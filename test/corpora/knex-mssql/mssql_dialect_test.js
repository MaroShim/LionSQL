// Knex.js MSSQL Dialect Test Queries (extracted from knex/test/unit/dialects/mssql.js)

const queries = [
  {
    name: "insert with output inserted.id",
    sql: "insert into [users] ([email], [name]) output inserted.[id] values ('test@knex.js', 'Knex User')"
  },
  {
    name: "select with offset and limit (fetch next)",
    sql: "select [id], [name] from [users] order by [id] asc offset 10 rows fetch next 5 rows only"
  },
  {
    name: "select top n",
    sql: "select top (5) [id], [name] from [users] where [active] = 1 order by [id] desc"
  },
  {
    name: "create migration tracking table",
    sql: "create table [knex_migrations] ([id] int identity(1,1) primary key, [name] nvarchar(255), [batch] int, [migration_time] datetime)"
  },
  {
    name: "knex lock update",
    sql: "create table [knex_migrations_lock] ([index] int identity(1,1) primary key, [is_locked] int)"
  }
];

module.exports = queries;
