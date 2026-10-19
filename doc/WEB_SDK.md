# LionSQL Official Web SDK & HTML Embedding Guide

The official **LionSQL Web SDK** provides a PocketBase-compatible, zero-dependency client library and HTML embedding patterns for Web Browsers, Node.js (v18+), Bun, Deno, Next.js, Nuxt, and Edge workers.

---

## 1. Quickstart & Installation

### Option A: Direct CDN / Script Tag (Zero Install)
Load the SDK directly from your running LionSQL server:
```html
<script src="http://127.0.0.1:8090/sdk/sqlion.js"></script>
<script>
  const client = new LionSQL('http://127.0.0.1:8090');
</script>
```

### Option B: ES Module / NPM / Bundler
```javascript
import LionSQL from './sdk/sqlion.js';

const client = new LionSQL('http://127.0.0.1:8090', {
  token: 'YOUR_JWT_OR_API_KEY' // optional initial token
});
```

---

## 2. Records CRUD & Querying

Each database table is represented as a collection service via `client.collection('table_name')`:

### 2.1. Paginated List with Filters & Sorting
```javascript
// Fetch page 1 with 20 items per page
const result = await client.collection('customers').getList(1, 20, {
  filter: 'balance >= 1000 && status = "active"',
  sort: '-created_at,balance'
});

console.log('Items:', result.items);
console.log('Total Records:', result.totalItems);
console.log('Total Pages:', result.totalPages);
```

### 2.2. Fetch Full List (Auto-Paging)
```javascript
// Automatically fetches all records across pages in batches of 100
const allCustomers = await client.collection('customers').getFullList({
  filter: 'status = "active"'
});
```

### 2.3. Fetch Single Record
```javascript
const record = await client.collection('customers').getOne(101);
console.log('Customer Name:', record.name);
```

### 2.4. Create Record
```javascript
const newCustomer = await client.collection('customers').create({
  name: 'Acme Corporation',
  balance: 50000,
  status: 'active'
});
console.log('Created ID:', newCustomer.id);
```

### 2.5. Update Record
```javascript
// PATCH update (modifies specified fields only)
const updated = await client.collection('customers').update(101, {
  balance: 52000
});
```

### 2.6. Delete Record
```javascript
await client.collection('customers').delete(101);
```

---

## 3. Realtime Live Event Streaming (SSE)

Subscribe to real-time database mutations without managing raw WebSocket or EventSource connections:

```javascript
// Subscribe to live events on 'customers' table
const unsubscribe = client.collection('customers').subscribe((event) => {
  console.log('Action:', event.action); // 'create' | 'update' | 'delete' | 'import'
  console.log('Changed Record:', event.record);
});

// When unmounting component or exiting:
unsubscribe();
```

---

## 4. T-SQL Stored Procedure Execution

Execute precompiled T-SQL stored procedures on the native VDBE virtual machine:

```javascript
const response = await client.exec('TransferFunds', {
  from: 1001,
  to: 1002,
  amount: 250
});

console.log('Result:', response);
```

---

## 5. Direct SQL Execution

Execute arbitrary SQL statements directly from client code:

```javascript
const result = await client.query('SELECT status, COUNT(*) as count FROM customers GROUP BY status;');
console.log('Aggregates:', result.rows);
```

---

## 6. Authentication & User Session

```javascript
// Superuser login
const authData = await client.auth.login('admin@lionsql.local', 'password');
console.log('Logged in as:', authData.user.email);
console.log('Token:', client.token);

// Check authentication status
if (client.isAuth) {
  const profile = await client.auth.me();
  console.log('Profile:', profile);
}

// Request password reset email
await client.auth.requestPasswordReset('user@example.com');

// Logout
await client.auth.logout();
```

---

## 7. Row-Level Security (RLS) Rules Management

Inspect and update declarative table security rules directly from client code:

```javascript
// Get table rules
const rules = await client.collection('customers').getRules();
console.log('List Rule:', rules.list_rule);

// Save table rules
await client.collection('customers').saveRules({
  list_rule: 'owner_id = @auth.id || @auth.role = "admin"',
  view_rule: 'owner_id = @auth.id || @auth.role = "admin"',
  create_rule: '@auth.id != ""',
  update_rule: 'owner_id = @auth.id || @auth.role = "admin"',
  delete_rule: 'owner_id = @auth.id || @auth.role = "admin"'
});
```

---

## 8. TypeScript Support

The SDK includes complete TypeScript definitions in `sdk/sqlion.d.ts`. Models can be strongly typed:

```typescript
interface Customer {
  id: number;
  name: string;
  balance: number;
  status: string;
}

const res = await client.collection<Customer>('customers').getList(1, 10);
// res.items is typed as Customer[]
```

---

## 9. Web & HTML Embedding Recipes

Because LionSQL serves its REST API, SQL endpoint, realtime SSE stream, and JavaScript SDK from a single executable on port `:8090`, you can embed dynamic query results into HTML pages with zero build tools (no Node.js, npm, or webpack required).

### 9.1. Pattern 1: Pure HTML + Vanilla `fetch()` (Zero-Dependency Single File)

Save this snippet as `query_table.html` and double-click to open in any web browser:

```html
<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <title>LionSQL Query Viewer</title>
  <style>
    body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; margin: 2rem; }
    table { border-collapse: collapse; width: 100%; max-width: 900px; box-shadow: 0 1px 3px rgba(0,0,0,0.1); }
    th, td { border: 1px solid #e2e8f0; padding: 10px 14px; text-align: left; }
    th { background: #f8fafc; font-weight: 600; color: #334155; }
    tr:nth-child(even) { background: #f8fafc; }
    .badge { display: inline-block; padding: 2px 8px; border-radius: 4px; font-size: 12px; background: #e0f2fe; color: #0369a1; }
  </style>
</head>
<body>
  <h2>Live Accounts Overview</h2>
  <table>
    <thead><tr id="tableHead"></tr></thead>
    <tbody id="tableBody"><tr><td>Loading query results...</td></tr></tbody>
  </table>

  <script>
    async function loadQuery() {
      try {
        const response = await fetch('http://localhost:8090/api/sql', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            sql: "SELECT id, holder, balance FROM accounts WHERE balance > 0 ORDER BY balance DESC"
          })
        });
        const data = await response.json(); // { columns: [...], rows: [...] }

        // Render table headers
        document.getElementById('tableHead').innerHTML = 
          data.columns.map(col => `<th>${col}</th>`).join('');

        // Render table rows
        document.getElementById('tableBody').innerHTML = data.rows.map(row => `
          <tr>
            ${data.columns.map(col => `<td>${row[col] ?? ''}</td>`).join('')}
          </tr>
        `).join('');
      } catch (err) {
        document.getElementById('tableBody').innerHTML = `<tr><td style="color:red">Error: ${err.message}</td></tr>`;
      }
    }

    loadQuery();
  </script>
</body>
</html>
```

### 9.2. Pattern 2: Web SDK + Realtime SSE Live Reactive Dashboard

This pattern connects directly to LionSQL's built-in Server-Sent Events stream. When any background client mutates data (via `psql`, `sqlcmd`, or REST), the web table updates automatically without manual page refreshing:

```html
<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <title>LionSQL Realtime Live Dashboard</title>
  <style>
    body { font-family: sans-serif; margin: 2rem; }
    .card { background: white; border: 1px solid #ddd; padding: 1rem; border-radius: 8px; max-width: 600px; margin-bottom: 1rem; }
    .status { color: #16a34a; font-weight: bold; }
  </style>
</head>
<body>
  <h2>Live Accounts Monitor <span class="status">&#x25CF; Connected</span></h2>
  <div id="container">Connecting...</div>

  <!-- Load Web SDK directly from LionSQL server (zero npm dependencies) -->
  <script type="module">
    import LionSQL from 'http://localhost:8090/sdk/sqlion.js';

    const db = new LionSQL('http://localhost:8090');

    // 1. Initial data fetch
    async function refresh() {
      const { items } = await db.collection('accounts').getList(1, 20, {
        sort: '-balance'
      });
      document.getElementById('container').innerHTML = items.map(account => `
        <div class="card">
          <h3>${account.holder}</h3>
          <p>Balance: $${Number(account.balance).toLocaleString()}</p>
        </div>
      `).join('');
    }

    await refresh();

    // 2. Real-time live synchronization via SSE
    db.collection('accounts').subscribe((event) => {
      console.log('Realtime event received:', event.action, event.record);
      refresh(); // Auto-refresh upon any insert/update/delete
    });
  </script>
</body>
</html>
```

### 9.3. Pattern 3: Declarative HTMX (Zero-JavaScript Polling & Embedding)

If you prefer declarative HTML without writing JavaScript, use [HTMX](https://htmx.org) with LionSQL's REST or SQL endpoints:

```html
<!DOCTYPE html>
<html>
<head>
  <script src="https://unpkg.com/htmx.org@1.9.10"></script>
</head>
<body>
  <h2>Auto-Refreshing Table (Every 5s)</h2>

  <!-- Automatically issues POST request on page load and every 5 seconds -->
  <div hx-post="http://localhost:8090/api/sql"
       hx-headers='{"Content-Type": "application/json"}'
       hx-vals='{"sql": "SELECT id, holder, balance FROM accounts ORDER BY id ASC"}'
       hx-trigger="load, every 5s"
       hx-target="#table-result">
  </div>

  <div id="table-result">Loading data...</div>
</body>
</html>
```

### 9.4. Pattern 4: T-SQL `FOR JSON PATH` Direct HTML Interpolation

LionSQL natively supports Microsoft T-SQL `FOR JSON PATH` queries. You can query structured JSON objects directly in SQL, avoiding client-side serialization:

```javascript
const res = await fetch('http://localhost:8090/api/sql', {
  method: 'POST',
  headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify({
    sql: "SELECT id, holder, balance FROM accounts FOR JSON PATH"
  })
});
const { rows } = await res.json();
const accounts = JSON.parse(rows[0]["JSON_F52E2B61-18A1-11d1-B105-00805F49916B"]);
console.log(accounts); // Array of formatted JSON objects
```

---

## 10. Instant Code Generation via Web Studio Developer API Preview

When exploring tables in the LionSQL Web Studio (`http://127.0.0.1:8090/_/`), click the **`</> API Preview`** button on any table view. 

The studio automatically analyzes your table's columns and generates ready-to-run **LionSQL SDK (JS/TS)**, **cURL**, **Fetch**, **Python**, and **Go** code snippets with:
- Correct collection names pre-filled: `db.collection('tableName')`
- Schema-matched insert payloads with realistic types and sample values
- Dynamic primary key lookup samples and query filter presets
- One-click clipboard copy (`📋 Copy Code`)


