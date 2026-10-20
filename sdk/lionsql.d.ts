/**
 * SQLion JavaScript / TypeScript Client SDK Type Definitions
 */

export interface SQLionOptions {
  token?: string;
  auth?: string;
  headers?: Record<string, string>;
}

export interface ListOptions {
  filter?: string;
  sort?: string;
  fields?: string;
  batch?: number;
}

export interface ListResult<T = any> {
  page: number;
  perPage: number;
  totalItems: number;
  totalPages: number;
  items: T[];
}

export interface TableRules {
  table?: string;
  list_rule?: string;
  view_rule?: string;
  create_rule?: string;
  update_rule?: string;
  delete_rule?: string;
}

export interface RealtimeMessage<T = any> {
  action: 'create' | 'update' | 'delete' | 'import';
  table: string;
  record: T;
  timestamp: string;
}

export declare class RecordService<T = any> {
  constructor(client: SQLion, collectionName: string);
  getList(page?: number, perPage?: number, options?: ListOptions): Promise<ListResult<T>>;
  getFullList(options?: ListOptions): Promise<T[]>;
  getOne(id: string | number, options?: ListOptions): Promise<T>;
  create(body: Partial<T>): Promise<T>;
  update(id: string | number, body: Partial<T>): Promise<T>;
  replace(id: string | number, body: Partial<T>): Promise<T>;
  delete(id: string | number): Promise<{ success: boolean }>;
  subscribe(callback: (msg: RealtimeMessage<T>) => void): () => void;
  unsubscribe(): void;
  getRules(): Promise<TableRules>;
  saveRules(rules: TableRules): Promise<TableRules>;
}

export declare class AuthService {
  constructor(client: SQLion);
  login(email: string, password: string): Promise<{ token: string; user: any }>;
  logout(): Promise<void>;
  me(): Promise<{ id: string; email: string; role: string }>;
  requestPasswordReset(email: string): Promise<{ success: boolean; message: string }>;
}

export declare class RealtimeService {
  constructor(client: SQLion);
  connect(): void;
  subscribe(topic: string, callback: (msg: RealtimeMessage) => void): () => void;
  unsubscribe(topic: string): void;
}

export declare class SQLionError extends Error {
  status: number;
  data: any;
  constructor(status: number, message: string, data?: any);
}

export default class SQLion {
  baseUrl: string;
  token: string;
  headers: Record<string, string>;
  auth: AuthService;
  realtime: RealtimeService;

  constructor(baseUrl?: string, options?: SQLionOptions);
  setToken(token: string): this;
  readonly isAuth: boolean;
  collection<T = any>(name: string): RecordService<T>;
  query(sql: string): Promise<any>;
  exec(procName: string, params?: Record<string, any>): Promise<any>;
  send(path: string, options?: RequestInit): Promise<any>;
}
