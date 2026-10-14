package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"sqlion/pkg/catalog"
	"sqlion/pkg/proc"
	"sqlion/pkg/storage"
)

func (s *Server) handleListProcs(w http.ResponseWriter, r *http.Request) {
	procs := s.catalog.ListProcedures()
	writeJSON(w, http.StatusOK, procs)
}

type registerProcReq struct {
	Script string `json:"script"`
}

func (s *Server) handleRegisterProc(w http.ResponseWriter, r *http.Request) {
	var req registerProcReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	trimmed := strings.TrimSpace(req.Script)
	upper := strings.ToUpper(trimmed)
	var procName string
	var paramDefs []catalog.ParamDef
	var jsCode string
	var orAlter bool
	var isAlter bool

	if strings.HasPrefix(upper, "CREATE ") || strings.HasPrefix(upper, "ALTER ") {
		plan, err := s.planCache.GetOrCompile(req.Script)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		procName = plan.Name
		orAlter = plan.OrAlter
		isAlter = plan.IsAlter
		paramDefs = make([]catalog.ParamDef, len(plan.Params))
		for i, p := range plan.Params {
			paramDefs[i] = catalog.ParamDef{Name: p.Name, Type: p.Type}
		}
		prog, compErr := proc.CompileProcedure(plan)
		if compErr != nil {
			writeError(w, http.StatusBadRequest, compErr)
			return
		}
		jsCode = prog.Disassemble()
		s.executor.Cache().PutByName(procName, prog)
	} else {
		writeError(w, http.StatusBadRequest, fmt.Errorf("procedure must begin with CREATE PROCEDURE, ALTER PROCEDURE, or CREATE OR ALTER PROCEDURE (T-SQL)"))
		return
	}

	procMeta := &catalog.Procedure{
		Name:   procName,
		Params: paramDefs,
		Body:   req.Script,
		JSCode: jsCode,
	}

	var regErr error
	if isAlter {
		regErr = s.catalog.AlterProcedure(procMeta)
	} else if orAlter {
		regErr = s.catalog.RegisterProcedure(procMeta)
	} else {
		regErr = s.catalog.CreateProcedure(procMeta)
	}

	if regErr != nil {
		if errors.Is(regErr, catalog.ErrProcExists) {
			writeError(w, http.StatusConflict, fmt.Errorf("procedure %q already exists. Use 'CREATE OR ALTER PROCEDURE' or 'ALTER PROCEDURE' to replace it", procName))
			return
		}
		if errors.Is(regErr, catalog.ErrProcNotFound) {
			writeError(w, http.StatusNotFound, fmt.Errorf("cannot alter procedure %q because it does not exist", procName))
			return
		}
		writeError(w, http.StatusInternalServerError, regErr)
		return
	}

	msg := fmt.Sprintf("Procedure %q registered and bytecode compiled successfully", procName)
	if isAlter || orAlter {
		msg = fmt.Sprintf("Procedure %q altered and bytecode compiled successfully", procName)
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"name":    procName,
		"params":  paramDefs,
		"js_code": jsCode,
		"message": msg,
	})
}

type execProcReq struct {
	Args map[string]any `json:"args"`
}

func (s *Server) handleExecProc(w http.ResponseWriter, r *http.Request) {
	procName := r.PathValue("name")

	prog, found := s.executor.Cache().GetByName(procName)
	if !found {
		// Try to compile from catalog
		procMeta, err := s.catalog.GetProcedure(procName)
		if err != nil {
			writeError(w, http.StatusNotFound, fmt.Errorf("procedure %q not found", procName))
			return
		}
		prog, err = s.executor.Cache().GetOrCompile(procMeta.Body)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		s.executor.Cache().PutByName(procName, prog)
	}

	var req execProcReq
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.Args == nil {
		req.Args = make(map[string]any)
	}

	// Normalize arguments so both "@arg" and "arg" work seamlessly
	normalizedArgs := make(map[string]any)
	for k, v := range req.Args {
		normalizedArgs[k] = v
		normalizedArgs[strings.TrimPrefix(k, "@")] = v
	}

	session, err := s.engine.NewSession()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer session.Close()

	res, err := s.executor.Run(prog, procName, normalizedArgs, session)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"success":     true,
		"return_code": res.ReturnValue,
		"duration_ms": float64(res.Duration.Microseconds()) / 1000.0,
		"logs":        res.Logs,
	})
}

func convertValue(v any, targetType storage.DataType) storage.Value {
	if v == nil {
		return storage.NullValue(targetType)
	}
	switch targetType {
	case storage.TypeInt:
		switch num := v.(type) {
		case float64:
			return storage.IntValue(int64(num))
		case int:
			return storage.IntValue(int64(num))
		case int64:
			return storage.IntValue(num)
		case string:
			if num == "" {
				return storage.NullValue(targetType)
			}
			var i int64
			fmt.Sscanf(num, "%d", &i)
			return storage.IntValue(i)
		}
	case storage.TypeVarChar:
		return storage.StringValue(fmt.Sprintf("%v", v))
	case storage.TypeFloat:
		switch num := v.(type) {
		case float64:
			return storage.FloatValue(num)
		case int:
			return storage.FloatValue(float64(num))
		case int64:
			return storage.FloatValue(float64(num))
		case string:
			if num == "" {
				return storage.NullValue(targetType)
			}
			f, _ := strconv.ParseFloat(num, 64)
			return storage.FloatValue(f)
		}
	case storage.TypeDecimal:
		switch num := v.(type) {
		case storage.Decimal:
			return storage.DecimalValueFromDecimal(num)
		case float64:
			return storage.DecimalValueFromDecimal(storage.NewDecimalFromFloat(num, 4))
		case int:
			return storage.DecimalValueFromDecimal(storage.NewDecimalFromInt(int64(num), 0))
		case int64:
			return storage.DecimalValueFromDecimal(storage.NewDecimalFromInt(num, 0))
		case string:
			if num == "" {
				return storage.NullValue(targetType)
			}
			return storage.DecimalValue(num)
		default:
			return storage.DecimalValue(fmt.Sprintf("%v", v))
		}
	case storage.TypeBool:
		switch b := v.(type) {
		case bool:
			return storage.BoolValue(b)
		case string:
			s := strings.ToLower(strings.TrimSpace(b))
			return storage.BoolValue(s == "true" || s == "1" || s == "t")
		case int, int64:
			return storage.BoolValue(fmt.Sprintf("%v", b) == "1")
		}
	case storage.TypeDateTime:
		switch t := v.(type) {
		case time.Time:
			return storage.DateTimeValue(t)
		case string:
			if t == "" {
				return storage.NullValue(targetType)
			}
			for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02"} {
				if parsed, err := time.Parse(layout, t); err == nil {
					return storage.DateTimeValue(parsed)
				}
			}
		}
	}
	return storage.StringValue(fmt.Sprintf("%v", v))
}
