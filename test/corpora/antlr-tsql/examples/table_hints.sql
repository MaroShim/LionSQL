-- ANTLR4 T-SQL table_hints.sql example
SELECT id, name
FROM users WITH (NOLOCK)
WHERE active = 1;

SELECT o.id, c.name
FROM orders o WITH (NOLOCK, FORCESEEK)
JOIN customers c WITH (NOLOCK) ON o.customer_id = c.id
OPTION (RECOMPILE);
