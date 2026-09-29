/**
 * SQLion Official JavaScript / TypeScript Client SDK
 * PocketBase-compatible high-performance client for SQLion Database.
 * 
 * Supports Browsers, Node.js (v18+), Bun, Deno, and Edge Runtimes.
 * @version 0.7.0
 * @license MIT
 */
(function (root, factory) {
  if (typeof define === 'function' && define.amd) {
    define([], factory);
  } else if (typeof module === 'object' && module.exports) {
    module.exports = factory();
  } else {
    root.SQLion = factory();
  }
}(typeof self !== 'undefined' ? self : this, function () {
  'use strict';

  class SQLionError extends Error {
    constructor(status, message, data = null) {
      super(message);
      this.name = 'SQLionError';
      this.status = status;
      this.data = data;
    }
  }

  class RecordService {
    constructor(client, collectionName) {
      this.client = client;
      this.collection = collectionName;
    }

    /**
     * Fetch paginated list of records with filter and sorting.
     */
    async getList(page = 1, perPage = 30, options = {}) {
      const params = new URLSearchParams();
      params.set('page', String(page));
      params.set('limit', String(perPage));
      if (options.filter) params.set('filter', options.filter);
      if (options.sort) params.set('sort', options.sort);
      if (options.fields) params.set('fields', options.fields);

      const endpoint = `/api/records/${encodeURIComponent(this.collection)}?${params.toString()}`;
      return this.client.send(endpoint, { method: 'GET' });
    }

    /**
     * Fetch all records across all pages sequentially.
     */
    async getFullList(options = {}) {
      const batchSize = options.batch || 100;
      let page = 1;
      let allItems = [];

      while (true) {
        const res = await this.getList(page, batchSize, options);
        const items = res.items || res.records || [];
        allItems = allItems.concat(items);
        if (items.length < batchSize || allItems.length >= (res.totalItems || res.total || 0)) {
          break;
        }
        page++;
      }
      return allItems;
    }

    /**
     * Fetch single record by primary key ID.
     */
    async getOne(id, options = {}) {
      const endpoint = `/api/records/${encodeURIComponent(this.collection)}/${encodeURIComponent(id)}`;
      return this.client.send(endpoint, { method: 'GET' });
    }

    /**
     * Insert a new record into table.
     */
    async create(body = {}) {
      const endpoint = `/api/records/${encodeURIComponent(this.collection)}`;
      return this.client.send(endpoint, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body)
      });
    }

    /**
     * Partially update an existing record.
     */
    async update(id, body = {}) {
      const endpoint = `/api/records/${encodeURIComponent(this.collection)}/${encodeURIComponent(id)}`;
      return this.client.send(endpoint, {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body)
      });
    }

    /**
     * Replace an existing record.
     */
    async replace(id, body = {}) {
      const endpoint = `/api/records/${encodeURIComponent(this.collection)}/${encodeURIComponent(id)}`;
      return this.client.send(endpoint, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body)
      });
    }

    /**
     * Delete record by primary key ID.
     */
    async delete(id) {
      const endpoint = `/api/records/${encodeURIComponent(this.collection)}/${encodeURIComponent(id)}`;
      return this.client.send(endpoint, { method: 'DELETE' });
    }

    /**
     * Subscribe to real-time change events for this table.
     * Returns an unsubscribe function.
     */
    subscribe(callback) {
      return this.client.realtime.subscribe(this.collection, callback);
    }

    /**
     * Unsubscribe all listeners from this table.
     */
    unsubscribe() {
      this.client.realtime.unsubscribe(this.collection);
    }

    /**
     * Fetch RLS security rules for this table.
     */
    async getRules() {
      const endpoint = `/api/tables/${encodeURIComponent(this.collection)}/rules`;
      return this.client.send(endpoint, { method: 'GET' });
    }

    /**
     * Save RLS security rules for this table.
     */
    async saveRules(rules = {}) {
      const endpoint = `/api/tables/${encodeURIComponent(this.collection)}/rules`;
      return this.client.send(endpoint, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(rules)
      });
    }
  }

  class AuthService {
    constructor(client) {
      this.client = client;
    }

    /**
     * Authenticate superuser and store bearer token.
     */
    async login(email, password) {
      const res = await this.client.send('/api/auth/login', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ email, password })
      });

      if (res && res.token) {
        this.client.setToken(res.token);
      }
      return res;
    }

    /**
     * Invalidate session and clear token.
     */
    async logout() {
      try {
        await this.client.send('/api/auth/logout', { method: 'POST' });
      } catch (e) {
        // ignore network error on logout
      }
      this.client.setToken('');
    }

    /**
     * Get identity and claims of current authenticated session.
     */
    async me() {
      return this.client.send('/api/auth/me', { method: 'GET' });
    }

    /**
     * Request password reset email.
     */
    async requestPasswordReset(email) {
      return this.client.send('/api/auth/request-password-reset', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ email })
      });
    }
  }

  class RealtimeService {
    constructor(client) {
      this.client = client;
      this.eventSource = null;
      this.subscriptions = new Map(); // topic -> Set<callback>
      this.reconnectTimeout = null;
    }

    connect() {
      if (this.eventSource || typeof EventSource === 'undefined') return;

      const url = `${this.client.baseUrl.replace(/\/$/, '')}/api/realtime`;
      try {
        this.eventSource = new EventSource(url);

        this.eventSource.onmessage = (e) => {
          try {
            const data = JSON.parse(e.data);
            const table = data.table;

            // Trigger specific table listeners
            if (table && this.subscriptions.has(table)) {
              this.subscriptions.get(table).forEach(cb => cb(data));
            }
            // Trigger wildcard listeners
            if (this.subscriptions.has('*')) {
              this.subscriptions.get('*').forEach(cb => cb(data));
            }
          } catch (err) {
            console.error('[SQLion Realtime] Parse error:', err);
          }
        };

        this.eventSource.onerror = () => {
          if (this.eventSource) {
            this.eventSource.close();
            this.eventSource = null;
          }
          clearTimeout(this.reconnectTimeout);
          this.reconnectTimeout = setTimeout(() => this.connect(), 3000);
        };
      } catch (err) {
        console.error('[SQLion Realtime] Connection error:', err);
      }
    }

    subscribe(topic, callback) {
      if (!this.subscriptions.has(topic)) {
        this.subscriptions.set(topic, new Set());
      }
      this.subscriptions.get(topic).add(callback);
      this.connect();

      return () => {
        const set = this.subscriptions.get(topic);
        if (set) {
          set.delete(callback);
          if (set.size === 0) {
            this.subscriptions.delete(topic);
          }
        }
      };
    }

    unsubscribe(topic) {
      this.subscriptions.delete(topic);
      if (this.subscriptions.size === 0 && this.eventSource) {
        this.eventSource.close();
        this.eventSource = null;
      }
    }
  }

  class SQLion {
    constructor(baseUrl = 'http://127.0.0.1:8090', options = {}) {
      this.baseUrl = baseUrl.replace(/\/$/, '');
      this.token = options.token || options.auth || '';
      this.headers = options.headers || {};

      this.auth = new AuthService(this);
      this.realtime = new RealtimeService(this);
    }

    /**
     * Set active JWT Bearer or API Key token.
     */
    setToken(token) {
      this.token = token;
      return this;
    }

    /**
     * Returns true if a client token is currently set.
     */
    get isAuth() {
      return Boolean(this.token);
    }

    /**
     * Get or create a record service for a table.
     */
    collection(name) {
      return new RecordService(this, name);
    }

    /**
     * Execute arbitrary SQL query directly on the server.
     */
    async query(sql) {
      return this.send('/api/sql', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ query: sql })
      });
    }

    /**
     * Execute a T-SQL stored procedure with parameters.
     */
    async exec(procName, params = {}) {
      return this.send('/api/procedures/execute', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ procedure: procName, params })
      });
    }

    /**
     * Core HTTP dispatch method.
     */
    async send(path, options = {}) {
      const url = `${this.baseUrl}${path.startsWith('/') ? path : '/' + path}`;
      const headers = {
        'Accept': 'application/json',
        ...this.headers,
        ...(options.headers || {})
      };

      if (this.token && !headers['Authorization']) {
        headers['Authorization'] = `Bearer ${this.token}`;
      }

      const res = await fetch(url, {
        ...options,
        headers
      });

      let json = null;
      const text = await res.text();
      try {
        json = text ? JSON.parse(text) : {};
      } catch (e) {
        json = { raw: text };
      }

      if (!res.ok) {
        const msg = json && (json.error || json.message) ? (json.error || json.message) : `HTTP ${res.status} ${res.statusText}`;
        throw new SQLionError(res.status, msg, json);
      }

      return json;
    }
  }

  return SQLion;
}));
