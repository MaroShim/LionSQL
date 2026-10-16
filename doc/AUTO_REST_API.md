# LionSQL Auto-Generated RESTful Records CRUD API

LionSQL provides instant, zero-code **Auto-Generated RESTful CRUD APIs** for all database tables. The moment a table is created (via T-SQL DDL, SQL migration, or Web Studio), standard REST endpoints are automatically exposed under `/api/records/:table`.

Frontend applications (React, Next.js, Vue, Flutter, iOS, Android) and backend microservices can interact directly with the database without writing boilerplate backend controllers, ORM models, or custom SQL wrappers.

---

## 1. Architectural Overview & Benefits

```
  [ Frontend Client / Mobile App / Microservice ]
                        │
                        ▼  HTTP REST (JSON)
  ┌────────────────────────────────────────────────────────┐
  │  GET    /api/records/:table?filter=...&sort=...&page=1 │
  │  GET    /api/records/:table/:id                        │
  │  POST   /api/records/:table                            │
  │  PATCH  /api/records/:table/:id                        │
  │  PUT    /api/records/:table/:id                        │
  │  DELETE /api/records/:table/:id                        │
  └────────────────────────────────────────────────────────┘
                        │
                        ▼
  ┌────────────────────────────────────────────────────────┐
  │  LionSQL Core Engine                                    │
  │  ├─ URL Query Filter Parser (pkg/api/records_filter.go) │
  │  ├─ Granular Table-Level API Key Authorization         │
  │  ├─ Storage Session & Concurrency Governor             │
  │  ├─ Schema-Aware Type Marshalling (Int, Float, Bool)   │
  │  └─ Realtime SSE Event Broadcast (ActionCreate/Update) │
  └────────────────────────────────────────────────────────┘
```

### Key Advantages:
1. **Zero Backend Code**: Immediate CRUD availability upon table creation.
2. **Standard RESTful Semantics**: Conventional HTTP methods (`GET`, `POST`, `PATCH`, `PUT`, `DELETE`) and status codes (`200`, `201`, `400`, `404`, `409`).
3. **Powerful URL Query Engine**: Complex filtering (`filter=balance>500 && status='active'`), multi-column sorting (`sort=-id,balance`), pagination (`page=1&limit=20`), and substring search.
4. **Schema-Aware JSON Typing**: Values are marshaled using exact database types (numbers for numeric columns, booleans for boolean columns, nulls for nullable columns) rather than flat string arrays.
5. **Auto Primary Key Generation**: Automatic snowflake/millisecond timestamp ID generation when inserting records into INT primary key tables.
6. **Built-in Realtime SSE**: Automatically triggers SSE events (`/api/realtime`) on every insert, update, and delete.

---

## 2. Standard Endpoint Reference

| Method | Endpoint | Description |
| :--- | :--- | :--- |
| **`GET`** | `/api/records/:table` | Paginated, filtered, and sorted list of records |
| **`GET`** | `/api/records/:table/:id` | Fetch single record by primary key |
| **`POST`** | `/api/records/:table` | Insert a new record (auto-generates PK if omitted) |
| **`PATCH`** | `/api/records/:table/:id` | Partially update an existing record by ID |
| **`PUT`** | `/api/records/:table/:id` | Update or replace an existing record by ID |
| **`DELETE`** | `/api/records/:table/:id` | Delete a single record by primary key |

> **Backward Compatibility Note**: The legacy `/api/tables/:name/records` and `/api/tables/:name/records/:pk` endpoints remain fully supported and share the exact same underlying filter and pagination engine.

---

## 3. Query Parameters for `GET /api/records/:table`

### 3.1. Query Filter Syntax (`filter=...`)

The filter engine evaluates boolean expressions directly against rows with type-aware comparisons:

#### Supported Operators
| Operator | Description | Example |
| :--- | :--- | :--- |
| `>` | Greater than (numeric/string) | `filter=balance>500` |
| `>=` | Greater than or equal | `filter=balance>=1000.5` |
| `<` | Less than | `filter=balance<100` |
| `<=` | Less than or equal | `filter=age<=65` |
| `=`, `==` | Equality (type-safe, case-insensitive string) | `filter=status='active'` or `filter=status=active` |
| `!=`, `<>` | Inequality | `filter=status!='inactive'` |
| `~` | Contains substring (case-insensitive) | `filter=name~'smith'` |
| `!~` | Does not contain substring | `filter=email!~'spam'` |
| `LIKE` | SQL LIKE pattern matching (`%`, `_`) | `filter=email LIKE '%@example.com'` |
| `IS NULL` | Null check | `filter=deleted_at IS NULL` or `filter=deleted_at=null` |
| `IS NOT NULL`| Non-null check | `filter=notes IS NOT NULL` or `filter=notes!=null` |

#### Logical Connectors
- **Conjunction (AND)**: `&&`, `AND` (case-insensitive), or `,` (comma)
  - `filter=balance>500 && status='active'`
  - `filter=balance>500,status=active`
- **Disjunction (OR)**: `||` or `OR`
  - `filter=status='active' || status='pending'`
- **Parentheses Grouping**:
  - `filter=(balance>1000 || status='vip') && country='US'`

#### Direct Query Parameter Shorthand
Parameters matching table column names can also be passed directly as query parameters:
```http
GET /api/records/customers?status=active&balance>=500
```

---

### 3.2. Sorting (`sort=...`)

Records can be sorted by one or more columns:
- Prefix `-`: Descending order (`DESC`).
- Prefix `+` or no prefix: Ascending order (`ASC`).
- Comma `,`: Multi-column sorting.

**Examples:**
- `sort=-id`: ID descending (newest first).
- `sort=balance`: Balance ascending.
- `sort=-status,balance`: Status descending, then balance ascending.

---

### 3.3. Pagination (`page` & `limit` / `perPage`)

- `page`: 1-based page number (default: `1`).
- `limit` (or `perPage`): Items per page (default: `30`, max: `500`).

**Response Envelope:**
```json
{
  "table": "customers",
  "page": 1,
  "limit": 20,
  "perPage": 20,
  "totalItems": 3,
  "totalPages": 1,
  "records": [
    { "id": 5, "name": "Evan", "balance": 600, "status": "active" },
    { "id": 3, "name": "Charlie", "balance": 800, "status": "active" },
    { "id": 1, "name": "Alice", "balance": 1000.5, "status": "active" }
  ],
  "items": [ ... ]
}
```
*(Both `records` and `items`, as well as `limit` and `perPage`, are provided for seamless integration with diverse frontend component libraries).*

---

### 3.4. Global Full-Text Substring Search (`search=...`)

`search=keyword` searches case-insensitively across all string and numeric columns of candidate rows:
```http
GET /api/records/customers?search=alice
```

---

## 4. End-to-End Walkthrough & Examples

### 4.1. Create a Table
Using T-SQL or REST:
```sql
CREATE TABLE customers (
    id INT PRIMARY KEY,
    name VARCHAR(100) NOT NULL,
    balance FLOAT NOT NULL,
    status VARCHAR(20) NOT NULL
);
```

### 4.2. Insert Records (`POST /api/records/:table`)
```bash
curl -X POST "http://localhost:8090/api/records/customers" \
  -H "Content-Type: application/json" \
  -d '{"id": 1, "name": "Alice", "balance": 1000.5, "status": "active"}'
```
*Response (`201 Created`):*
```json
{
  "id": 1,
  "name": "Alice",
  "balance": 1000.5,
  "status": "active"
}
```

*Auto-generating primary key (omitting `id`):*
```bash
curl -X POST "http://localhost:8090/api/records/customers" \
  -H "Content-Type: application/json" \
  -d '{"name": "Evan", "balance": 600.0, "status": "active"}'
```

### 4.3. List with Filter, Sort & Pagination
```bash
curl -X GET "http://localhost:8090/api/records/customers?filter=balance>500&sort=-id&page=1&limit=20"
```

### 4.4. Fetch Single Record by ID (`GET /api/records/:table/:id`)
```bash
curl -X GET "http://localhost:8090/api/records/customers/1"
```
*Response (`200 OK`):*
```json
{
  "id": 1,
  "name": "Alice",
  "balance": 1000.5,
  "status": "active"
}
```

### 4.5. Partial Update (`PATCH /api/records/:table/:id`)
```bash
curl -X PATCH "http://localhost:8090/api/records/customers/1" \
  -H "Content-Type: application/json" \
  -d '{"balance": 1250.0}'
```
*Response (`200 OK`):*
```json
{
  "id": 1,
  "name": "Alice",
  "balance": 1250.0,
  "status": "active"
}
```

### 4.6. Delete Record (`DELETE /api/records/:table/:id`)
```bash
curl -X DELETE "http://localhost:8090/api/records/customers/1"
```
*Response (`200 OK`):*
```json
{
  "success": true,
  "message": "record deleted",
  "id": "1"
}
```

---

## 5. Client Integration Code Samples

### 5.1. JavaScript / TypeScript (React, Vue, Next.js)

```typescript
const BASE_URL = 'http://localhost:8090';

// 1. Fetch filtered and sorted records
async function getActiveCustomers(minBalance = 500, page = 1) {
  const query = new URLSearchParams({
    filter: `balance>${minBalance} && status='active'`,
    sort: '-id',
    page: String(page),
    limit: '20'
  });

  const res = await fetch(`${BASE_URL}/api/records/customers?${query}`, {
    headers: { 'Authorization': 'Bearer <API_KEY>' }
  });
  const data = await res.json();
  return data.records; // Array of customer objects
}

// 2. Insert new record
async function createCustomer(name: string, balance: number, status = 'active') {
  const res = await fetch(`${BASE_URL}/api/records/customers`, {
    method: 'POST',
    headers: {
      'Authorization': 'Bearer <API_KEY>',
      'Content-Type': 'application/json'
    },
    body: JSON.stringify({ name, balance, status })
  });
  return await res.json();
}
```

### 5.2. Flutter / Dart

```dart
import 'dart:convert';
import 'package:http/http.dart' as http;

final String baseUrl = 'http://localhost:8090';

Future<List<dynamic>> fetchCustomers() async {
  final uri = Uri.parse('$baseUrl/api/records/customers').replace(
    queryParameters: {
      'filter': 'balance>500',
      'sort': '-id',
      'page': '1',
      'limit': '20',
    },
  );

  final res = await http.get(uri, headers: {
    'Authorization': 'Bearer <API_KEY>',
  });

  if (res.statusCode == 200) {
    final body = jsonDecode(res.body);
    return body['records'];
  }
  throw Exception('Failed to load customers: ${res.statusCode}');
}
```

### 5.3. Python

```python
import requests

BASE_URL = "http://localhost:8090"
HEADERS = {"Authorization": "Bearer <API_KEY>"}

# List records
params = {
    "filter": "balance>500,status=active",
    "sort": "-id",
    "page": 1,
    "limit": 20
}
resp = requests.get(f"{BASE_URL}/api/records/customers", headers=HEADERS, params=params)
print(resp.json()["records"])

# Create record
new_customer = {"name": "Frank", "balance": 1500.0, "status": "active"}
created = requests.post(f"{BASE_URL}/api/records/customers", headers=HEADERS, json=new_customer)
print(created.json())
```

---

## 6. Authentication & Granular Access Control

When authentication is enabled (`-auth` flag):
1. **API Key Authentication**: Pass key via header:
   ```http
   X-API-Key: sqlion_key_abc123...
   ```
   or
   ```http
   Authorization: ApiKey sqlion_key_abc123...
   ```
2. **Role Enforcement**:
   - `read_only`: Allowed on `GET /api/records/:table` and `GET /api/records/:table/:id`. Write methods (`POST`, `PATCH`, `PUT`, `DELETE`) return `403 Forbidden`.
   - `read_write`: Allowed on all CRUD operations for designated `allowed_tables`.
   - `admin`: Superuser permissions across all database tables.

---

## 7. Realtime SSE Integration

Client applications can subscribe to live table updates via Server-Sent Events:
```javascript
const eventSource = new EventSource('http://localhost:8090/api/realtime');

eventSource.onmessage = (e) => {
  const event = JSON.parse(e.data);
  // event: { action: "create" | "update" | "delete", table: "customers", record: { ... }, id: "1" }
  console.log('Realtime change detected:', event);
};
```
Whenever a record is created, modified, or deleted via `/api/records/:table`, LionSQL broadcasts the event instantaneously to all connected subscribers.
