# LionSQL Internals: Schema-Aware Compact Binary Tuple Format (`pkg/storage/tuple.go`)

This document details the binary record serialization format, bit-level memory layout, encoding specifications, performance optimizations, and architectural lineage implemented in LionSQL's storage layer.

---

## 1. Overview & Architectural Motivation

In high-concurrency OLTP engines, record serialization and deserialization represent critical hot paths that directly determine transaction throughput, CPU cache efficiency, and disk I/O amplification.

Traditional database engines and document stores frequently rely on generic serialization protocols such as JSON, BSON, or MessagePack. However, generic formats introduce significant liabilities in relational workloads:
1. **Redundant Field Name Metadata**: Storing field names (`"id"`, `"holder"`, `"balance"`) with every row wastes 60% to 80% of storage and memory bandwidth.
2. **Dynamic Type Tag Overhead**: Repeating type tags per field imposes unnecessary branch mispredictions and decoding overhead.
3. **Heavy Heap Allocation**: Deserializing generic formats typically instantiates intermediate `map[string]any` dictionaries, triggering high Go garbage collection (GC) pressure.

LionSQL eliminates these overheads via a **Schema-Aware Compact Binary Tuple Codec** (`pkg/storage/tuple.go`). Because table schemas are already persisted in the database catalog, tuples omit column names and sequential type descriptors entirely, serializing only minimal headers, null bitmaps, and dense value payloads.

---

## 2. Binary Layout Specification

A LionSQL binary tuple (V2) is a contiguous byte slice structured with a 4-byte 32-bit aligned fixed header followed by a null bitmap and value payloads:

```
┌─────────────────┬─────────────────┬──────────────────────────┬──────────────────────────┬────────────────────────────────────────────────────────┐
│ Version Header  │ Flags           │ Physical Column Count    │ Null Bitmap              │ Sequential Column Payloads                             │
│ (1 Byte: 0x02)  │ (1 Byte: 0x00)  │ (2 Bytes: uint16 LE)     │ (ceil(N / 8) Bytes)      │ (Only non-NULL columns, strictly ordered by schema)   │
└─────────────────┴─────────────────┴──────────────────────────┴──────────────────────────┴────────────────────────────────────────────────────────┘
```

### 2.1. Version Header (1 Byte)
* **Value**: `0x02` (`TupleCodecVersion` / `TupleCodecV2`)
* **Purpose**: Identifies the binary layout specification.
* **Integrity Guard**: Decoders immediately reject buffers whose initial byte does not match `0x02` with `ErrInvalidTupleHeader` (Fail-Fast firewall). Legacy V1 formats (`0x01`) are rejected cleanly with zero silent memory corruption.

### 2.2. Flags Header (1 Byte)
* **Value**: `0x00` (`TupleFlagNone` by default)
* **Bit Allocation**:
  * `0x01` (`TupleFlagNoNulls`): Reserved for tuples with zero NULL values (null bitmap can be omitted in future optimizations).
  * `0x02` (`TupleFlagCompressed`): Reserved for row-level payload compression (e.g., ZSTD/Snappy for large JSON/Text rows).
  * `0x04` (`TupleFlagToast`): Reserved for out-of-line TOAST/LOB overflow pointers for very large binary objects (> 16KB).
  * `0x08` (`TupleFlagExtended`): Reserved for extended header metadata.
* **Extensibility**: Allows future engine features (compression, TOAST, encryption) to be activated without altering the binary format version or invalidating existing data pages.

### 2.3. Physical Column Count (`encodedCols uint16`, 2 Bytes)
* **Value**: Little-Endian unsigned 16-bit integer (range $0 \sim 65,535$).
* **Self-Describing Layout**: Embeds the exact number of columns physically encoded when the tuple was written.
* **Instant Schema Evolution ($O(1)$)**:
  * **Tail ADD COLUMN**: When a table schema has $N > \text{encodedCols}$ columns, columns $0 \sim \text{encodedCols}-1$ are decoded from the tuple bytes, and newly added columns $\text{encodedCols} \sim N-1$ are instantly padded from the schema default (`col.Default`) or `NULL` without rewriting the table or reading past buffer boundaries.
  * **Tail DROP COLUMN**: When the terminal column is dropped ($N < \text{encodedCols}$), decoders process columns $0 \sim N-1$ and cleanly ignore trailing bytes.
  * **Middle DROP Safety**: Dropping non-terminal (middle) columns on non-empty tables is strictly rejected with an explicit error (`ErrDropColumnNotTail`) to prevent silent column-offset misalignment.

### 2.4. Null Bitmap ($\lceil N / 8 \rceil$ Bytes)
* **Length Calculation**: For a tuple with `encodedCols` columns, the bitmap consumes `(encodedCols + 7) / 8` bytes.
  - $1 \sim 8$ columns: 1 byte
  - $9 \sim 16$ columns: 2 bytes
  - $17 \sim 24$ columns: 3 bytes
* **Bit Representation**:
  - Column index $i$ corresponds to byte `4 + (i / 8)` and bit `1 << (i % 8)`.
  - **Bit = 1**: The column value is `NULL`.
  - **Bit = 0**: The column value is non-NULL and present in the payload section.
* **Zero Payload Overhead**: When a column is marked NULL in the bitmap, **zero bytes** are emitted into the payload area. Sparse tables with numerous NULL columns achieve optimal storage density.

### 2.5. Sequential Column Payloads
Non-NULL columns are serialized in the exact ordinal sequence defined by `TableSchema.Columns`. Each column payload adheres to the encoding rules described below:

| Storage Type | Go Native Type | Encoding Scheme | Byte Length |
|---|---|---|---|
| **`TypeInt`** | `int64` | ZigZag LEB128 Variable-Length Integer (`binary.PutVarint`) | 1 to 10 bytes (typically 1~2 bytes for small integers) |
| **`TypeFloat`** | `float64` | IEEE 754 Little-Endian 64-bit Float (`binary.LittleEndian.PutUint64(math.Float64bits(v))`) | 8 bytes (fixed) |
| **`TypeBool`** | `bool` | Single Byte Flag (`0x01` = true, `0x00` = false) | 1 byte (fixed) |
| **`TypeDateTime`** | `time.Time` | Little-Endian 64-bit UTC Unix Nanoseconds (`v.UTC().UnixNano()`) | 8 bytes (fixed) |
| **`TypeDate`** | `time.Time` | Little-Endian 64-bit UTC Unix Seconds (`v.UTC().Unix()`) | 8 bytes (fixed) |
| **`TypeTime`** | `time.Time` | Little-Endian 64-bit Nanoseconds since midnight | 8 bytes (fixed) |
| **`TypeVarChar`** / **`TypeText`** | `string` | Unsigned Varint Byte Length (`PutUvarint`) + UTF-8 Byte Sequence | 1~5 bytes length + $L$ bytes |
| **`TypeDecimal`** | `storage.Decimal` | Unsigned Varint Byte Length (`PutUvarint`) + ASCII Exact Numeric String | 1~5 bytes length + $L$ bytes |
| **`TypeUUID`** | `string` | Unsigned Varint Byte Length (`PutUvarint`) + Canonical UUID String | 1~5 bytes length + $L$ bytes |
| **`TypeJSON`** | `string` | Unsigned Varint Byte Length (`PutUvarint`) + Minified JSON Bytes | 1~5 bytes length + $L$ bytes |
| **`TypeBinary`** | `[]byte` | Unsigned Varint Byte Length (`PutUvarint`) + Raw Binary Bytes | 1~5 bytes length + $L$ bytes |

---

## 3. Detailed Data Type Encodings

### 3.1. Integer Compression (`TypeInt`)
LionSQL applies Google Protocol Buffers-style ZigZag encoding combined with LEB128 unsigned varints (`binary.PutVarint`):
- Small positive and negative integers (e.g., $0, 1, -1, 100$) map to small unsigned values and pack into 1 or 2 bytes instead of 8 bytes.
- Typical OLTP Primary Keys and foreign key IDs ($1 \sim 100,000$) require only 2 to 3 bytes.

### 3.2. Variable-Length String & Binary Encoding
Rather than null-terminated strings (`\0`), which prevent binary transparency and require linear byte scanning, LionSQL enforces length-prefixed encoding:
- The byte length $L$ is encoded as an unsigned LEB128 varint (`binary.PutUvarint`).
- Followed immediately by the exact $L$ bytes.
- Decoders read the length in $O(1)$, allocate or slice the underlying buffer directly without string scanning, and preserve embedded null characters (`\x00`).

### 3.3. Exact Numeric Storage (`TypeDecimal`)
High-precision monetary and financial amounts (`storage.Decimal`, e.g., TPC-C balances, order totals) are stored losslessly:
- The exact numeric representation is preserved via canonical ASCII bytes prefixed by length.
- Eliminates IEEE 754 binary floating-point rounding inaccuracies while maintaining zero serialization distortion.

---

## 4. Architectural Lineage & Design Influences

The architecture of LionSQL's compact binary tuple derives from battle-tested principles established in modern relational and distributed database kernels:

```
                  [ Architectural Lineage of LionSQL Binary Tuple ]
                                          │
       ┌──────────────────────────────────┼──────────────────────────────────┐
       ▼                                  ▼                                  ▼
[ PostgreSQL HeapTuple ]       [ CockroachDB / TiDB ]              [ Google Protobuf & SQLite ]
- `t_bits` Null Bitmap         - Schema-Aware Tuple Packing        - ZigZag Varint Compression
- Zero bytes for NULL fields   - Zero column names in row bodies   - Length-prefixed string buffers
```

1. **PostgreSQL Heap Tuple Header (`t_bits`)**:
   - Reference: PostgreSQL `HeapTupleHeaderData` (`src/include/access/htup_details.h`).
   - PostgreSQL places a null bitmap (`t_bits`) immediately after the fixed header, allowing NULL attributes to consume zero payload storage. LionSQL adopts this exact rule via the prefix null bitmap.
2. **Schema-Aware Row Encoding in Distributed KV Engines**:
   - Reference: CockroachDB row-level encoding, TiDB Table-to-KV mapping.
   - Key-Value-backed SQL engines decouple schema metadata from row payloads. Since the database catalog already maintains column types and indices, rows persist only raw values in column definition order.
3. **SQLite & Protocol Buffers Varint Compaction**:
   - Reference: SQLite Record Format, Google Protobuf LEB128.
   - Variable-length integer encoding drastically reduces database footprint on NVMe drives and improves page density in buffer pools.

---

## 5. Storage Engine Integration

LionSQL applies this binary tuple codec across both storage engines in distinct, optimized roles:

### 5.1. WiredTiger Storage Backend (`pkg/storage/wiredtiger/`)
WiredTiger organizes tables as B+Trees where keys and values are opaque byte arrays:
- **B+Tree Key**: Compact binary encoded Primary Key (e.g., integer or string PK bytes).
- **B+Tree Value**: The complete compact binary tuple `[0x01] + [Null Bitmap] + [Payloads]`.
- **Write Path (`Insert`, `Update`)**:
  `storage.EncodeTupleToBuffer` serializes the incoming `storage.Record` directly into a pooled scratch buffer, writing the resulting bytes into the WiredTiger cursor.
- **Read Path (`Get`, `Scan`)**:
  When reading from a cursor, `storage.DecodeTupleToRow` deserializes the on-disk byte slice directly into an indexed `storage.Row` without intermediate map allocations.

### 5.2. In-Memory OCC Engine (`pkg/storage/memengine/`)
The in-memory OCC engine partitions responsibilities between fast RAM execution and durable disk recovery:
1. **In-RAM Representation (`table_data.go`)**:
   - To eliminate serialization CPU cycles during high-frequency in-memory transactions (1,250,000+ tpmC), records remain in memory as pre-parsed `storage.Row` structs (`[]storage.Value`).
   - Column values are resolved in $O(1)$ by slice index without byte parsing.
2. **On-Disk Persistence (WAL, AOF, PITR Backups)**:
   - When appending write mutations to the Write-Ahead Log (WAL), compaction logs (AOF), or point-in-time recovery snapshots, rows are encoded into compact binary tuples via `EncodeTupleToBuffer`.
   - On crash recovery or restart, WAL workers stream these binary tuples, using `DecodeTupleToRow` to rebuild in-memory tables at high throughput.

---

## 6. Zero-Allocation & Concurrency Performance

To satisfy LionSQL's engineering golden rules regarding zero-allocation hot paths (Rule 30) and responsive latency budgets (Rule 31), the tuple codec incorporates several low-level optimizations:

### 6.1. Scratch Buffer Pooling (`sync.Pool`)
Encoding binary tuples requires allocating destination byte slices. Instead of triggering dynamic heap allocations per mutation, `pkg/storage/tuple.go` maintains a global buffer pool:

```go
var tupleBufferPool = sync.Pool{
    New: func() any {
        b := make([]byte, 0, 512)
        return &b
    },
}
```
Workers borrow a 512-byte scratch slice, append tuple bytes, transfer or write the slice to storage, and return the buffer to the pool via `defer`. This completely eliminates heap churn on write paths.

### 6.2. Direct `Row` Passthrough (`DecodeTupleToRow`)
Legacy database layers typically decode bytes into `map[string]any` dictionaries, causing:
1. Heap allocation for every map entry.
2. String hashing overhead for column lookups.
3. Interface boxing (`any`) for primitive values.

LionSQL's `DecodeTupleToRow(schema, data)` bypasses maps entirely:
- Pre-allocates a slice of `storage.Value` matching `len(schema.Columns)`.
- Fills values directly by index using unboxed primitive fields (`IVal int64`, `FVal float64`, `SVal string`, `BVal bool`).
- Upstream Volcano iterators and VDBE execution frames consume `storage.Row` directly, achieving zero map overhead across scans.

---

## 7. Verification & Test Suite

The compact binary tuple format is validated by dedicated hermetic test suites:
- `pkg/storage/tuple_test.go`:
  - `TestTuple_EncodeDecodeRoundTrip`: Verifies lossless round-trip serialization across all supported data types, including NULLs and mixed schemas.
  - `TestTuple_NullHandling`: Validates multi-byte null bitmaps and zero-payload storage for NULL fields.
  - `TestTuple_DecodeToRow_Performance`: Benchmarks decode speed, ensuring sub-microsecond row deserialization.
- `pkg/storage/memengine/wal_test.go`:
  - Verifies WAL binary tuple encoding and crash replay recovery.
- `pkg/storage/wiredtiger/wt_engine_test.go`:
  - Validates B+Tree leaf value persistence and cursor scan parity.
