-- ANTLR4 T-SQL merge.sql example
MERGE target_table AS t
USING source_table AS s
ON (t.id = s.id)
WHEN MATCHED AND s.status = 'DELETED' THEN
    DELETE
WHEN MATCHED THEN
    UPDATE SET t.val = s.val, t.updated_at = GETDATE()
WHEN NOT MATCHED THEN
    INSERT (id, val, updated_at) VALUES (s.id, s.val, GETDATE());
