package engine

import (
	"strconv"
	"strings"

	"lionsql/pkg/catalog"
	"lionsql/pkg/proc"
	"lionsql/pkg/proc/runtime"
	"lionsql/pkg/sqlutil"
	"lionsql/pkg/storage"
)

// PreparedProcInfo contains metadata for executing a stored procedure via prepared statement fast-path.
type PreparedProcInfo struct {
	ProcName   string
	Program    *proc.Program
	ParamNames []string           // Positional 0-indexed placeholder mapping to procedure param names (e.g. "@w_id")
	ParamTypes []storage.DataType // Types of parameters
}

// ResolvePreparedProcedure analyzes a query string to determine if it is a prepared invocation
// of a stored procedure (e.g. EXEC procName @arg1=$1, @arg2=$2 or EXEC procName $1, $2).
// If valid, it returns the compiled VDBE Program and placeholder parameter mapping.
func ResolvePreparedProcedure(query string, cat *catalog.Catalog, cache *runtime.PlanCache) *PreparedProcInfo {
	if cat == nil {
		return nil
	}
	trimmed := strings.TrimRight(strings.TrimSpace(query), ";")
	trimmed = strings.TrimSpace(trimmed)
	if !sqlutil.HasAnyPrefixKeyword(trimmed, "EXEC", "EXECUTE", "CALL") {
		return nil
	}

	procName, args := ParseProcArgs(trimmed)
	if procName == "" {
		return nil
	}
	lowerName := strings.ToLower(procName)
	if isBuiltinSystemProcedure(lowerName) {
		// Built-in system procedures have custom coordinator logic; do not bypass to generic VDBE
		return nil
	}

	procMeta, err := cat.GetProcedure(procName)
	if err != nil || procMeta == nil || procMeta.Body == "" {
		return nil
	}

	var prog *proc.Program
	if cache != nil {
		var found bool
		prog, found = cache.GetByName(procName)
		if !found {
			prog, err = cache.GetOrCompile(procMeta.Body)
			if err != nil || prog == nil {
				return nil
			}
			cache.PutByName(procName, prog)
		}
	} else {
		prog, err = proc.Compile(procMeta.Body)
		if err != nil || prog == nil {
			return nil
		}
	}

	type paramInfo struct {
		name string
		typ  storage.DataType
	}
	procParamsByName := make(map[string]paramInfo, len(procMeta.Params))
	for _, p := range procMeta.Params {
		clean := strings.ToLower(strings.TrimPrefix(p.Name, "@"))
		procParamsByName[clean] = paramInfo{name: "@" + clean, typ: p.Type}
	}

	maxSlot := -1
	type slotBinding struct {
		slot int
		name string
		typ  storage.DataType
	}
	var bindings []slotBinding

	for i, rawArg := range args {
		arg := strings.TrimSpace(rawArg)
		if arg == "" {
			continue
		}

		targetParamName := ""
		var targetType storage.DataType = storage.TypeUnknown
		valPart := arg

		if eqIdx := strings.IndexByte(arg, '='); eqIdx != -1 {
			left := strings.TrimSpace(arg[:eqIdx])
			right := strings.TrimSpace(arg[eqIdx+1:])
			cleanLeft := strings.ToLower(strings.TrimPrefix(left, "@"))
			if pi, ok := procParamsByName[cleanLeft]; ok {
				targetParamName = pi.name
				targetType = pi.typ
			}
			valPart = right
		} else if i < len(procMeta.Params) {
			p := procMeta.Params[i]
			targetParamName = "@" + strings.ToLower(strings.TrimPrefix(p.Name, "@"))
			targetType = p.Type
		}

		if targetParamName == "" {
			continue
		}

		slotIdx := extractPlaceholderIndex(valPart, i)
		if slotIdx >= 0 {
			if slotIdx > maxSlot {
				maxSlot = slotIdx
			}
			bindings = append(bindings, slotBinding{slot: slotIdx, name: targetParamName, typ: targetType})
		}
	}

	if maxSlot < 0 {
		if len(args) == 0 {
			return &PreparedProcInfo{
				ProcName: procName,
				Program:  prog,
			}
		}
		return nil
	}

	paramNames := make([]string, maxSlot+1)
	paramTypes := make([]storage.DataType, maxSlot+1)
	for _, b := range bindings {
		if b.slot >= 0 && b.slot < len(paramNames) {
			paramNames[b.slot] = b.name
			paramTypes[b.slot] = b.typ
		}
	}

	return &PreparedProcInfo{
		ProcName:   procName,
		Program:    prog,
		ParamNames: paramNames,
		ParamTypes: paramTypes,
	}
}

func extractPlaceholderIndex(val string, defaultPos int) int {
	val = strings.TrimSpace(val)
	if strings.HasPrefix(val, "$") && len(val) > 1 {
		if idx, err := strconv.Atoi(val[1:]); err == nil && idx > 0 {
			return idx - 1
		}
	}
	if (strings.HasPrefix(val, "@p") || strings.HasPrefix(val, "@P")) && len(val) > 2 {
		if idx, err := strconv.Atoi(val[2:]); err == nil && idx > 0 {
			return idx - 1
		}
	}
	if val == "?" {
		return defaultPos
	}
	return -1
}

func isBuiltinSystemProcedure(lowerName string) bool {
	switch lowerName {
	case "sp_help", "sp_spaceused", "sp_who", "sp_who2", "sp_lock", "sp_rename",
		"sp_tables", "sp_columns", "sp_pkeys", "sp_fkeys", "sp_reset_connection",
		"sp_executesql", "sp_prepare", "sp_execute", "sp_unprepare", "sp_send_dbmail",
		"sp_add_job", "sp_add_jobstep", "sp_add_jobschedule", "sp_start_job", "sp_delete_job",
		"sp_babelfish_configure", "sp_syspolicy_configure", "sp_syspolicy_set_config_enabled",
		"sp_syspolicy_set_config_history_retention":
		return true
	default:
		return false
	}
}
