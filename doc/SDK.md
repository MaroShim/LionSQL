# SQLion Official JavaScript & TypeScript Client SDK

The official **SQLion Client SDK** provides a PocketBase-compatible, zero-dependency client library for Web Browsers, Node.js (v18+), Bun, Deno, Next.js, Nuxt, and Edge workers.

---

## 1. Quickstart & Installation

### Option A: Direct CDN / Script Tag (Zero Install)
Load the SDK directly from your running SQLion server:
```html
<script src="http://127.0.0.1:8090/sdk/sqlion.js"></script>
<script>
  const client = new SQLion('http://127.0.0.1:8090');
</script>
```

### Option B: ES Module / NPM / Bundler
```javascript
import SQLion from './sdk/sqlion.js';

const client = new SQLion('http://127.0.0.1:8090', {
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
const authData = await client.auth.login('admin@sqlion.local', 'password');
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
