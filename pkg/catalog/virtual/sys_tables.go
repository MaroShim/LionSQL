package virtual

// sysViewAliases is a table-driven lookup set for recognized Microsoft SQL Server sys.* catalog views and aliases.
var sysViewAliases = map[string]struct{}{
	"sys.tables": {}, "systables": {}, "sys_tables": {},
	"sys.columns": {}, "syscolumns": {}, "sys_columns": {},
	"sys.indexes": {}, "sysindexes": {}, "sys_indexes": {},
	"sys.index_columns": {}, "sysindexcolumns": {}, "sys_index_columns": {},
	"sys.databases": {}, "sysdatabases": {}, "sys_databases": {},
	"sys.procedures": {}, "sysprocedures": {}, "sys_procedures": {},
	"sys.parameters": {}, "sysparameters": {}, "sys_parameters": {},
	"sys.table_stats": {}, "systablestats": {}, "sys_table_stats": {},
	"sys.column_stats": {}, "syscolumnstats": {}, "sys_column_stats": {},
	"sys.schemas": {}, "sysschemas": {}, "sys_schemas": {},
	"sys.objects": {}, "sysobjects": {}, "sys_objects": {},
	"sys.views": {}, "sysviews": {}, "sys_views": {},
	"sys.types": {}, "systypes": {}, "sys_types": {},
	"sys.partitions": {}, "system.partitions": {}, "syspartitions": {}, "sys_partitions": {},
	"sys.computed_columns": {}, "syscomputedcolumns": {}, "sys_computed_columns": {},
	"sys.triggers": {}, "systriggers": {}, "sys_triggers": {},
	"sys.foreign_keys": {}, "sysforeignkeys": {}, "sys_foreign_keys": {},
	"sys.foreign_key_columns": {}, "sysforeignkeycolumns": {}, "sys_foreign_key_columns": {},
	"sys.configurations": {}, "sysconfigurations": {}, "sys_configurations": {},
	"sys.dm_exec_sessions": {}, "sysdmexecsessions": {}, "sys_dm_exec_sessions": {},
	"sys.dm_exec_connections": {}, "sysdmexecconnections": {}, "sys_dm_exec_connections": {},
	"sys.database_principals": {}, "sysdatabaseprincipals": {}, "sys_database_principals": {},
	"sys.server_principals": {}, "sysserverprincipals": {}, "sys_server_principals": {},
	"sys.dm_os_sys_info": {}, "sysdmosysinfo": {}, "sys_dm_os_sys_info": {},
	"sys.dm_os_host_info": {}, "sysdmoshostinfo": {}, "sys_dm_os_host_info": {},
	"sys.endpoints": {}, "sysendpoints": {}, "sys_endpoints": {},
	"sys.database_files": {}, "sysdatabasefiles": {}, "sys_database_files": {},
	"sys.master_files": {}, "sysmasterfiles": {}, "sys_master_files": {},
	"sys.all_objects": {}, "sysallobjects": {}, "sys_all_objects": {},
	"sys.all_columns": {}, "sysallcolumns": {}, "sys_all_columns": {},
	"sys.all_views": {}, "sysallviews": {}, "sys_all_views": {},
	"sys.syscomments": {}, "syscomments": {}, "sys_syscomments": {},
	"sys.sql_modules": {}, "syssqlmodules": {}, "sys_sql_modules": {},
	"fn_helpcollations": {}, "sys.fn_helpcollations": {},
}

// IsSysCatalogTable reports whether norm identifies any registered sys.* view or alias in the table.
func IsSysCatalogTable(norm string) bool {
	_, ok := sysViewAliases[norm]
	return ok
}
