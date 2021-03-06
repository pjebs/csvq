package query

import (
	"bytes"
	"context"
	"os"
	"strings"
	"time"

	"github.com/mithrandie/csvq/lib/cmd"
	"github.com/mithrandie/csvq/lib/excmd"
	"github.com/mithrandie/csvq/lib/json"
	"github.com/mithrandie/csvq/lib/parser"
	"github.com/mithrandie/csvq/lib/value"

	"github.com/mithrandie/ternary"
)

type filterRecord struct {
	view        *View
	recordIndex int

	fieldReferenceIndices map[string]int
}

type Filter struct {
	tx *Transaction

	records []filterRecord

	variables VariableScopes
	tempViews TemporaryViewScopes
	cursors   CursorScopes
	functions UserDefinedFunctionScopes

	inlineTables InlineTableNodes
	aliases      AliasNodes

	recursiveTable    *parser.InlineTable
	recursiveTmpView  *View
	tmpViewIsAccessed bool

	checkAvailableParallelRoutine bool

	cachedFilePath map[string]string
	now            time.Time
}

type ContainsSubstitusion struct{}

func (c *ContainsSubstitusion) Error() string {
	return "contains substitusion"
}

func NewFilter(tx *Transaction) *Filter {
	return NewFilterWithScopes(
		tx,
		VariableScopes{NewVariableMap()},
		TemporaryViewScopes{{}},
		CursorScopes{{}},
		UserDefinedFunctionScopes{{}},
	)
}

func NewFilterWithScopes(tx *Transaction, variableScopes VariableScopes, tempViewScopes TemporaryViewScopes, cursorScopes CursorScopes, functionScopes UserDefinedFunctionScopes) *Filter {
	return &Filter{
		tx:        tx,
		variables: variableScopes,
		tempViews: tempViewScopes,
		cursors:   cursorScopes,
		functions: functionScopes,
	}
}

func NewFilterForRecord(parentFilter *Filter, view *View, recordIndex int) *Filter {
	f := &Filter{
		tx: parentFilter.tx,
		records: []filterRecord{
			{
				view:                  view,
				recordIndex:           recordIndex,
				fieldReferenceIndices: make(map[string]int),
			},
		},
	}
	f.Merge(parentFilter)
	return f
}

func NewFilterForSequentialEvaluation(parentFilter *Filter, view *View) *Filter {
	f := &Filter{
		tx: parentFilter.tx,
		records: []filterRecord{
			{
				view:                  view,
				recordIndex:           -1,
				fieldReferenceIndices: make(map[string]int),
			},
		},
	}
	f.Merge(parentFilter)
	return f
}

func (f *Filter) Merge(filter *Filter) {
	f.records = append(f.records, filter.records...)
	f.variables = filter.variables
	f.tempViews = filter.tempViews
	f.cursors = filter.cursors
	f.functions = filter.functions
	f.inlineTables = filter.inlineTables
	f.aliases = filter.aliases
	f.cachedFilePath = filter.cachedFilePath
	f.now = filter.now
}

func (f *Filter) CreateChildScope() *Filter {
	child := NewFilterWithScopes(
		f.tx,
		append(VariableScopes{NewVariableMap()}, f.variables...),
		append(TemporaryViewScopes{{}}, f.tempViews...),
		append(CursorScopes{{}}, f.cursors...),
		append(UserDefinedFunctionScopes{{}}, f.functions...),
	)
	child.cachedFilePath = f.cachedFilePath
	child.now = f.now
	return child
}

func (f *Filter) ResetCurrentScope() {
	f.variables[0].variables.Range(func(k interface{}, v interface{}) bool {
		f.variables[0].variables.Delete(k)
		return true
	})
	for k := range f.tempViews[0] {
		delete(f.tempViews[0], k)
	}
	for k := range f.cursors[0] {
		delete(f.cursors[0], k)
	}
	for k := range f.functions[0] {
		delete(f.functions[0], k)
	}
}

func (f *Filter) CreateNode() *Filter {
	filter := &Filter{
		tx:               f.tx,
		records:          f.records,
		variables:        f.variables,
		tempViews:        f.tempViews,
		cursors:          f.cursors,
		functions:        f.functions,
		inlineTables:     append(InlineTableNodes{{}}, f.inlineTables...),
		aliases:          append(AliasNodes{{}}, f.aliases...),
		recursiveTable:   f.recursiveTable,
		recursiveTmpView: f.recursiveTmpView,
		cachedFilePath:   f.cachedFilePath,
		now:              f.now,
	}

	if filter.cachedFilePath == nil {
		filter.cachedFilePath = make(map[string]string)
	}
	if filter.now.IsZero() {
		filter.now = cmd.Now()
	}

	return filter
}

func (f *Filter) storeFilePath(identifier string, fpath string) {
	if f.cachedFilePath != nil {
		f.cachedFilePath[identifier] = fpath
	}
}

func (f *Filter) loadFilePath(identifier string) (string, bool) {
	if f.cachedFilePath != nil {
		if p, ok := f.cachedFilePath[identifier]; ok {
			return p, true
		}
	}
	return "", false
}

func (f *Filter) LoadInlineTable(ctx context.Context, clause parser.WithClause) error {
	return f.inlineTables.Load(ctx, f, clause)
}

func (f *Filter) Evaluate(ctx context.Context, expr parser.QueryExpression) (value.Primary, error) {
	if ctx.Err() != nil {
		return nil, NewContextIsDone(ctx.Err().Error())
	}

	if expr == nil {
		return value.NewTernary(ternary.TRUE), nil
	}

	var val value.Primary
	var err error

	switch expr.(type) {
	case parser.PrimitiveType:
		return expr.(parser.PrimitiveType).Value, nil
	case parser.Parentheses:
		val, err = f.Evaluate(ctx, expr.(parser.Parentheses).Expr)
	case parser.FieldReference, parser.ColumnNumber:
		val, err = f.evalFieldReference(expr)
	case parser.Arithmetic:
		val, err = f.evalArithmetic(ctx, expr.(parser.Arithmetic))
	case parser.UnaryArithmetic:
		val, err = f.evalUnaryArithmetic(ctx, expr.(parser.UnaryArithmetic))
	case parser.Concat:
		val, err = f.evalConcat(ctx, expr.(parser.Concat))
	case parser.Comparison:
		val, err = f.evalComparison(ctx, expr.(parser.Comparison))
	case parser.Is:
		val, err = f.evalIs(ctx, expr.(parser.Is))
	case parser.Between:
		val, err = f.evalBetween(ctx, expr.(parser.Between))
	case parser.Like:
		val, err = f.evalLike(ctx, expr.(parser.Like))
	case parser.In:
		val, err = f.evalIn(ctx, expr.(parser.In))
	case parser.Any:
		val, err = f.evalAny(ctx, expr.(parser.Any))
	case parser.All:
		val, err = f.evalAll(ctx, expr.(parser.All))
	case parser.Exists:
		val, err = f.evalExists(ctx, expr.(parser.Exists))
	case parser.Subquery:
		val, err = f.evalSubqueryForValue(ctx, expr.(parser.Subquery))
	case parser.Function:
		val, err = f.evalFunction(ctx, expr.(parser.Function))
	case parser.AggregateFunction:
		val, err = f.evalAggregateFunction(ctx, expr.(parser.AggregateFunction))
	case parser.ListFunction:
		val, err = f.evalListFunction(ctx, expr.(parser.ListFunction))
	case parser.CaseExpr:
		val, err = f.evalCaseExpr(ctx, expr.(parser.CaseExpr))
	case parser.Logic:
		val, err = f.evalLogic(ctx, expr.(parser.Logic))
	case parser.UnaryLogic:
		val, err = f.evalUnaryLogic(ctx, expr.(parser.UnaryLogic))
	case parser.Variable:
		val, err = f.variables.Get(expr.(parser.Variable))
	case parser.EnvironmentVariable:
		val = value.NewString(os.Getenv(expr.(parser.EnvironmentVariable).Name))
	case parser.RuntimeInformation:
		val, err = GetRuntimeInformation(f.tx, expr.(parser.RuntimeInformation))
	case parser.VariableSubstitution:
		if f.checkAvailableParallelRoutine {
			err = &ContainsSubstitusion{}
		} else {
			val, err = f.variables.Substitute(ctx, f, expr.(parser.VariableSubstitution))
		}
	case parser.CursorStatus:
		val, err = f.evalCursorStatus(expr.(parser.CursorStatus))
	case parser.CursorAttrebute:
		val, err = f.evalCursorAttribute(expr.(parser.CursorAttrebute))
	case parser.Placeholder:
		val, err = f.evalPlaceholder(ctx, expr.(parser.Placeholder))
	default:
		return nil, NewInvalidValueExpressionError(expr)
	}

	return val, err
}

func (f *Filter) EvaluateSequentially(ctx context.Context, fn func(*Filter, int) error, expr interface{}) error {
	if expr == nil || f.canUseMultithreading(ctx, expr) {
		header := f.records[0].view.Header
		recordSet := f.records[0].view.RecordSet
		isGrouped := f.records[0].view.isGrouped
		f.records = f.records[1:]

		gm := NewGoroutineTaskManager(len(recordSet), -1, f.tx.Flags.CPU)
		for i := 0; i < gm.Number; i++ {
			gm.Add()
			go func(thIdx int) {
				start, end := gm.RecordRange(thIdx)
				filter := NewFilterForSequentialEvaluation(
					f,
					&View{
						Tx:        f.tx,
						Header:    header,
						RecordSet: recordSet[start:end],
						isGrouped: isGrouped,
					},
				)
				filter.init()

				for filter.next() {
					if gm.HasError() || ctx.Err() != nil {
						break
					}

					if err := fn(filter, start+filter.currentIndex()); err != nil {
						gm.SetError(err)
						break
					}
				}

				gm.Done()
			}(i)
		}
		gm.Wait()

		if gm.HasError() {
			return gm.Err()
		}
		if ctx.Err() != nil {
			return NewContextIsDone(ctx.Err().Error())
		}
	} else {
		f.init()
		for f.next() {
			if err := fn(f, f.currentIndex()); err != nil {
				return err
			}
		}
	}
	return nil
}

func (f *Filter) next() bool {
	f.records[0].recordIndex++

	if f.records[0].view.Len() <= f.records[0].recordIndex {
		return false
	}
	return true
}

func (f *Filter) init() {
	f.records[0].recordIndex = -1
}

func (f *Filter) currentIndex() int {
	return f.records[0].recordIndex
}

func (f *Filter) canUseMultithreading(ctx context.Context, expr interface{}) bool {
	if 0 < len(f.records) && f.records[0].view != nil && 0 < f.records[0].view.Len() {
		f.init()
		f.checkAvailableParallelRoutine = true
		defer func() {
			f.checkAvailableParallelRoutine = false
		}()
		f.next()

		if qe, ok := expr.(parser.QueryExpression); ok {
			_, err := f.Evaluate(ctx, qe)

			if err != nil {
				if _, ok := err.(*ContainsSubstitusion); ok {
					return false
				}
			}
		} else if elist, ok := expr.([]parser.QueryExpression); ok {
			for _, expr := range elist {
				_, err := f.Evaluate(ctx, expr)
				if err != nil {
					if _, ok := err.(*ContainsSubstitusion); ok {
						return false
					}
				}
			}
		}
	}
	return true
}

func (f *Filter) evalFieldReference(expr parser.QueryExpression) (value.Primary, error) {
	exprStr := expr.String()

	var p value.Primary
	for _, v := range f.records {
		if v.fieldReferenceIndices != nil {
			if idx, ok := v.fieldReferenceIndices[exprStr]; ok {
				p = v.view.RecordSet[v.recordIndex][idx].Value()
				break
			}
		}

		idx, err := v.view.FieldIndex(expr)
		if err == nil {
			if v.view.isGrouped && v.view.Header[idx].IsFromTable && !v.view.Header[idx].IsGroupKey {
				return nil, NewFieldNotGroupKeyError(expr)
			}
			p = v.view.RecordSet[v.recordIndex][idx].Value()
			if v.fieldReferenceIndices != nil {
				v.fieldReferenceIndices[exprStr] = idx
			}
			break
		}

		if _, ok := err.(*FieldAmbiguousError); ok {
			return nil, err
		}
	}
	if p == nil {
		return nil, NewFieldNotExistError(expr)
	}
	return p, nil
}

func (f *Filter) evalArithmetic(ctx context.Context, expr parser.Arithmetic) (value.Primary, error) {
	lhs, err := f.Evaluate(ctx, expr.LHS)
	if err != nil {
		return nil, err
	}
	if value.IsNull(lhs) {
		return value.NewNull(), nil
	}

	rhs, err := f.Evaluate(ctx, expr.RHS)
	if err != nil {
		return nil, err
	}

	return Calculate(lhs, rhs, expr.Operator), nil
}

func (f *Filter) evalUnaryArithmetic(ctx context.Context, expr parser.UnaryArithmetic) (value.Primary, error) {
	ope, err := f.Evaluate(ctx, expr.Operand)
	if err != nil {
		return nil, err
	}

	if pi := value.ToInteger(ope); !value.IsNull(pi) {
		val := pi.(value.Integer).Raw()
		switch expr.Operator.Token {
		case '-':
			val = val * -1
		}
		return value.NewInteger(val), nil
	}

	pf := value.ToFloat(ope)
	if value.IsNull(pf) {
		return value.NewNull(), nil
	}

	val := pf.(value.Float).Raw()

	switch expr.Operator.Token {
	case '-':
		val = val * -1
	}

	return value.ParseFloat64(val), nil
}

func (f *Filter) evalConcat(ctx context.Context, expr parser.Concat) (value.Primary, error) {
	items := make([]string, len(expr.Items))
	for i, v := range expr.Items {
		s, err := f.Evaluate(ctx, v)
		if err != nil {
			return nil, err
		}
		s = value.ToString(s)
		if value.IsNull(s) {
			return value.NewNull(), nil
		}
		items[i] = s.(value.String).Raw()
	}
	return value.NewString(strings.Join(items, "")), nil
}

func (f *Filter) evalComparison(ctx context.Context, expr parser.Comparison) (value.Primary, error) {
	var t ternary.Value

	lhs, err := f.evalRowValue(ctx, expr.LHS)
	if err != nil {
		return nil, err
	}
	if lhs == nil {
		return value.NewTernary(ternary.UNKNOWN), nil
	}

	if 1 == len(lhs) {
		lhsVal := lhs[0]

		if value.IsNull(lhsVal) {
			return value.NewTernary(ternary.UNKNOWN), nil
		}

		rhs, err := f.Evaluate(ctx, expr.RHS)
		if err != nil {
			return nil, err
		}

		t = value.Compare(lhsVal, rhs, expr.Operator, f.tx.Flags.DatetimeFormat)
	} else {
		rhs, err := f.evalRowValue(ctx, expr.RHS.(parser.RowValue))
		if err != nil {
			return nil, err
		}

		t, err = value.CompareRowValues(lhs, rhs, expr.Operator, f.tx.Flags.DatetimeFormat)
		if err != nil {
			return nil, NewRowValueLengthInComparisonError(expr.RHS.(parser.RowValue), len(lhs))
		}
	}

	return value.NewTernary(t), nil
}

func (f *Filter) evalIs(ctx context.Context, expr parser.Is) (value.Primary, error) {
	lhs, err := f.Evaluate(ctx, expr.LHS)
	if err != nil {
		return nil, err
	}
	rhs, err := f.Evaluate(ctx, expr.RHS)
	if err != nil {
		return nil, err
	}

	t := Is(lhs, rhs)
	if expr.IsNegated() {
		t = ternary.Not(t)
	}
	return value.NewTernary(t), nil
}

func (f *Filter) evalBetween(ctx context.Context, expr parser.Between) (value.Primary, error) {
	var t ternary.Value

	lhs, err := f.evalRowValue(ctx, expr.LHS)
	if err != nil {
		return nil, err
	}
	if lhs == nil {
		return value.NewTernary(ternary.UNKNOWN), nil
	}

	if 1 == len(lhs) {
		lhsVal := lhs[0]

		if value.IsNull(lhsVal) {
			return value.NewTernary(ternary.UNKNOWN), nil
		}

		low, err := f.Evaluate(ctx, expr.Low)
		if err != nil {
			return nil, err
		}

		lowResult := value.GreaterOrEqual(lhsVal, low, f.tx.Flags.DatetimeFormat)
		if lowResult == ternary.FALSE {
			t = ternary.FALSE
		} else {
			high, err := f.Evaluate(ctx, expr.High)
			if err != nil {
				return nil, err
			}

			highResult := value.LessOrEqual(lhsVal, high, f.tx.Flags.DatetimeFormat)
			t = ternary.And(lowResult, highResult)
		}
	} else {
		low, err := f.evalRowValue(ctx, expr.Low.(parser.RowValue))
		if err != nil {
			return nil, err
		}
		lowResult, err := value.CompareRowValues(lhs, low, ">=", f.tx.Flags.DatetimeFormat)
		if err != nil {
			return nil, NewRowValueLengthInComparisonError(expr.Low.(parser.RowValue), len(lhs))
		}

		if lowResult == ternary.FALSE {
			t = ternary.FALSE
		} else {
			high, err := f.evalRowValue(ctx, expr.High.(parser.RowValue))
			if err != nil {
				return nil, err
			}

			highResult, err := value.CompareRowValues(lhs, high, "<=", f.tx.Flags.DatetimeFormat)
			if err != nil {
				return nil, NewRowValueLengthInComparisonError(expr.High.(parser.RowValue), len(lhs))
			}

			t = ternary.And(lowResult, highResult)
		}
	}

	if expr.IsNegated() {
		t = ternary.Not(t)
	}
	return value.NewTernary(t), nil
}

func (f *Filter) valuesForRowValueListComparison(ctx context.Context, lhs parser.QueryExpression, values parser.QueryExpression) (value.RowValue, []value.RowValue, error) {
	var rowValue value.RowValue
	var list []value.RowValue
	var err error

	rowValue, err = f.evalRowValue(ctx, lhs)
	if err != nil {
		return rowValue, list, err
	}

	if rowValue != nil && 1 < len(rowValue) {
		list, err = f.evalRowValueList(ctx, values)
	} else {
		list, err = f.evalArray(ctx, values)
	}

	return rowValue, list, err
}

func (f *Filter) evalIn(ctx context.Context, expr parser.In) (value.Primary, error) {
	val, list, err := f.valuesForRowValueListComparison(ctx, expr.LHS, expr.Values)
	if err != nil {
		return nil, err
	}

	t, err := Any(val, list, "=", f.tx.Flags.DatetimeFormat)
	if err != nil {
		if subquery, ok := expr.Values.(parser.Subquery); ok {
			return nil, NewSelectFieldLengthInComparisonError(subquery, len(val))
		} else if jsonQuery, ok := expr.Values.(parser.JsonQuery); ok {
			return nil, NewRowValueLengthInComparisonError(jsonQuery, len(val))
		}

		rvlist, _ := expr.Values.(parser.RowValueList)
		rverr, _ := err.(*RowValueLengthInListError)
		return nil, NewRowValueLengthInComparisonError(rvlist.RowValues[rverr.Index], len(val))
	}

	if expr.IsNegated() {
		t = ternary.Not(t)
	}
	return value.NewTernary(t), nil
}

func (f *Filter) evalAny(ctx context.Context, expr parser.Any) (value.Primary, error) {
	val, list, err := f.valuesForRowValueListComparison(ctx, expr.LHS, expr.Values)
	if err != nil {
		return nil, err
	}

	t, err := Any(val, list, expr.Operator, f.tx.Flags.DatetimeFormat)
	if err != nil {
		if subquery, ok := expr.Values.(parser.Subquery); ok {
			return nil, NewSelectFieldLengthInComparisonError(subquery, len(val))
		} else if jsonQuery, ok := expr.Values.(parser.JsonQuery); ok {
			return nil, NewRowValueLengthInComparisonError(jsonQuery, len(val))
		}

		rvlist, _ := expr.Values.(parser.RowValueList)
		rverr, _ := err.(*RowValueLengthInListError)
		return nil, NewRowValueLengthInComparisonError(rvlist.RowValues[rverr.Index], len(val))
	}
	return value.NewTernary(t), nil
}

func (f *Filter) evalAll(ctx context.Context, expr parser.All) (value.Primary, error) {
	val, list, err := f.valuesForRowValueListComparison(ctx, expr.LHS, expr.Values)
	if err != nil {
		return nil, err
	}

	t, err := All(val, list, expr.Operator, f.tx.Flags.DatetimeFormat)
	if err != nil {
		if subquery, ok := expr.Values.(parser.Subquery); ok {
			return nil, NewSelectFieldLengthInComparisonError(subquery, len(val))
		} else if jsonQuery, ok := expr.Values.(parser.JsonQuery); ok {
			return nil, NewRowValueLengthInComparisonError(jsonQuery, len(val))
		}

		rvlist, _ := expr.Values.(parser.RowValueList)
		rverr, _ := err.(*RowValueLengthInListError)
		return nil, NewRowValueLengthInComparisonError(rvlist.RowValues[rverr.Index], len(val))
	}
	return value.NewTernary(t), nil
}

func (f *Filter) evalLike(ctx context.Context, expr parser.Like) (value.Primary, error) {
	lhs, err := f.Evaluate(ctx, expr.LHS)
	if err != nil {
		return nil, err
	}
	pattern, err := f.Evaluate(ctx, expr.Pattern)
	if err != nil {
		return nil, err
	}

	t := Like(lhs, pattern)
	if expr.IsNegated() {
		t = ternary.Not(t)
	}
	return value.NewTernary(t), nil
}

func (f *Filter) evalExists(ctx context.Context, expr parser.Exists) (value.Primary, error) {
	view, err := Select(ctx, f, expr.Query.Query)
	if err != nil {
		return nil, err
	}
	if view.RecordLen() < 1 {
		return value.NewTernary(ternary.FALSE), nil
	}
	return value.NewTernary(ternary.TRUE), nil
}

func (f *Filter) evalSubqueryForValue(ctx context.Context, expr parser.Subquery) (value.Primary, error) {
	view, err := Select(ctx, f, expr.Query)
	if err != nil {
		return nil, err
	}

	if 1 < view.FieldLen() {
		return nil, NewSubqueryTooManyFieldsError(expr)
	}

	if 1 < view.RecordLen() {
		return nil, NewSubqueryTooManyRecordsError(expr)
	}

	if view.RecordLen() < 1 {
		return value.NewNull(), nil
	}

	return view.RecordSet[0][0].Value(), nil
}

func (f *Filter) evalFunction(ctx context.Context, expr parser.Function) (value.Primary, error) {
	name := strings.ToUpper(expr.Name)

	if _, ok := Functions[name]; !ok && name != "CALL" && name != "NOW" && name != "JSON_OBJECT" {
		udfn, err := f.functions.Get(expr, name)
		if err != nil {
			return nil, NewFunctionNotExistError(expr, expr.Name)
		}
		if udfn.IsAggregate {
			aggrdcl := parser.AggregateFunction{
				BaseExpr: expr.BaseExpr,
				Name:     expr.Name,
				Args:     expr.Args,
			}
			return f.evalAggregateFunction(ctx, aggrdcl)
		}

		if err = udfn.CheckArgsLen(expr, expr.Name, len(expr.Args)); err != nil {
			return nil, err
		}
	}

	if name == "JSON_OBJECT" {
		return JsonObject(ctx, f, expr)
	}

	args := make([]value.Primary, len(expr.Args))
	for i, v := range expr.Args {
		arg, err := f.Evaluate(ctx, v)
		if err != nil {
			return nil, err
		}
		args[i] = arg
	}

	if name == "CALL" {
		return Call(ctx, expr, args)
	} else if name == "NOW" {
		return Now(f, expr, args)
	}

	if fn, ok := Functions[name]; ok {
		return fn(expr, args, f.tx.Flags)
	}

	udfn, _ := f.functions.Get(expr, name)
	return udfn.Execute(ctx, f, args)
}

func (f *Filter) evalAggregateFunction(ctx context.Context, expr parser.AggregateFunction) (value.Primary, error) {
	var aggfn func([]value.Primary, *cmd.Flags) value.Primary
	var udfn *UserDefinedFunction
	var useUserDefined bool
	var err error

	uname := strings.ToUpper(expr.Name)
	if fn, ok := AggregateFunctions[uname]; ok {
		aggfn = fn
	} else {
		if udfn, err = f.functions.Get(expr, uname); err != nil || !udfn.IsAggregate {
			return nil, NewFunctionNotExistError(expr, expr.Name)
		}
		useUserDefined = true
	}

	if useUserDefined {
		if err = udfn.CheckArgsLen(expr, expr.Name, len(expr.Args)-1); err != nil {
			return nil, err
		}
	} else {
		if len(expr.Args) != 1 {
			return nil, NewFunctionArgumentLengthError(expr, expr.Name, []int{1})
		}
	}

	if len(f.records) < 1 {
		return nil, NewUnpermittedFunctionStatementError(expr, expr.Name)
	}

	if !f.records[0].view.isGrouped {
		return nil, NewNotGroupingRecordsError(expr, expr.Name)
	}

	listExpr := expr.Args[0]
	if _, ok := listExpr.(parser.AllColumns); ok {
		listExpr = parser.NewIntegerValue(1)
	}

	if uname == "COUNT" {
		if _, ok := listExpr.(parser.PrimitiveType); ok {
			return value.NewInteger(int64(f.records[0].view.RecordSet[f.records[0].recordIndex].GroupLen())), nil
		}
	}

	view := NewViewFromGroupedRecord(f.records[0])
	list, err := view.ListValuesForAggregateFunctions(ctx, expr, listExpr, expr.IsDistinct(), f)
	if err != nil {
		return nil, err
	}

	if useUserDefined {
		argsExprs := expr.Args[1:]
		args := make([]value.Primary, len(argsExprs))
		for i, v := range argsExprs {
			arg, err := f.Evaluate(ctx, v)
			if err != nil {
				return nil, err
			}
			args[i] = arg
		}
		return udfn.ExecuteAggregate(ctx, f, list, args)
	}

	return aggfn(list, f.tx.Flags), nil
}

func (f *Filter) evalListFunction(ctx context.Context, expr parser.ListFunction) (value.Primary, error) {
	var separator string
	var err error

	switch strings.ToUpper(expr.Name) {
	case "JSON_AGG":
		err = f.checkArgsForJsonAgg(expr)
	default: // LISTAGG
		separator, err = f.checkArgsForListFunction(ctx, expr)
	}

	if err != nil {
		return nil, err
	}

	if len(f.records) < 1 {
		return nil, NewUnpermittedFunctionStatementError(expr, expr.Name)
	}

	if !f.records[0].view.isGrouped {
		return nil, NewNotGroupingRecordsError(expr, expr.Name)
	}

	view := NewViewFromGroupedRecord(f.records[0])
	if expr.OrderBy != nil {
		err := view.OrderBy(ctx, expr.OrderBy.(parser.OrderByClause))
		if err != nil {
			return nil, err
		}
	}

	list, err := view.ListValuesForAggregateFunctions(ctx, expr, expr.Args[0], expr.IsDistinct(), f)
	if err != nil {
		return nil, err
	}

	switch strings.ToUpper(expr.Name) {
	case "JSON_AGG":
		return JsonAgg(list), nil
	}
	return ListAgg(list, separator), nil
}

func (f *Filter) checkArgsForListFunction(ctx context.Context, expr parser.ListFunction) (string, error) {
	var separator string

	if expr.Args == nil || 2 < len(expr.Args) {
		return "", NewFunctionArgumentLengthError(expr, expr.Name, []int{1, 2})
	}

	if len(expr.Args) == 2 {
		p, err := f.Evaluate(ctx, expr.Args[1])
		if err != nil {
			return separator, NewFunctionInvalidArgumentError(expr, expr.Name, "the second argument must be a string")
		}
		s := value.ToString(p)
		if value.IsNull(s) {
			return separator, NewFunctionInvalidArgumentError(expr, expr.Name, "the second argument must be a string")
		}
		separator = s.(value.String).Raw()
	}
	return separator, nil
}

func (f *Filter) checkArgsForJsonAgg(expr parser.ListFunction) error {
	if 1 != len(expr.Args) {
		return NewFunctionArgumentLengthError(expr, expr.Name, []int{1})
	}
	return nil
}

func (f *Filter) evalCaseExpr(ctx context.Context, expr parser.CaseExpr) (value.Primary, error) {
	var val value.Primary
	var err error
	if expr.Value != nil {
		val, err = f.Evaluate(ctx, expr.Value)
		if err != nil {
			return nil, err
		}
	}

	for _, v := range expr.When {
		when := v.(parser.CaseExprWhen)
		var t ternary.Value

		cond, err := f.Evaluate(ctx, when.Condition)
		if err != nil {
			return nil, err
		}

		if val == nil {
			t = cond.Ternary()
		} else {
			t = value.Equal(val, cond, f.tx.Flags.DatetimeFormat)
		}

		if t == ternary.TRUE {
			result, err := f.Evaluate(ctx, when.Result)
			if err != nil {
				return nil, err
			}
			return result, nil
		}
	}

	if expr.Else == nil {
		return value.NewNull(), nil
	}
	result, err := f.Evaluate(ctx, expr.Else.(parser.CaseExprElse).Result)
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (f *Filter) evalLogic(ctx context.Context, expr parser.Logic) (value.Primary, error) {
	lhs, err := f.Evaluate(ctx, expr.LHS)
	if err != nil {
		return nil, err
	}
	switch expr.Operator.Token {
	case parser.AND:
		if lhs.Ternary() == ternary.FALSE {
			return value.NewTernary(ternary.FALSE), nil
		}
	case parser.OR:
		if lhs.Ternary() == ternary.TRUE {
			return value.NewTernary(ternary.TRUE), nil
		}
	}

	rhs, err := f.Evaluate(ctx, expr.RHS)
	if err != nil {
		return nil, err
	}

	var t ternary.Value
	switch expr.Operator.Token {
	case parser.AND:
		t = ternary.And(lhs.Ternary(), rhs.Ternary())
	case parser.OR:
		t = ternary.Or(lhs.Ternary(), rhs.Ternary())
	}
	return value.NewTernary(t), nil
}

func (f *Filter) evalUnaryLogic(ctx context.Context, expr parser.UnaryLogic) (value.Primary, error) {
	ope, err := f.Evaluate(ctx, expr.Operand)
	if err != nil {
		return nil, err
	}

	var t ternary.Value
	switch expr.Operator.Token {
	case parser.NOT, '!':
		t = ternary.Not(ope.Ternary())
	}
	return value.NewTernary(t), nil
}

func (f *Filter) evalCursorStatus(expr parser.CursorStatus) (value.Primary, error) {
	var t ternary.Value
	var err error

	switch expr.Type {
	case parser.OPEN:
		t, err = f.cursors.IsOpen(expr.Cursor)
		if err != nil {
			return nil, err
		}
	case parser.RANGE:
		t, err = f.cursors.IsInRange(expr.Cursor)
		if err != nil {
			return nil, err
		}
	}

	if !expr.Negation.IsEmpty() {
		t = ternary.Not(t)
	}
	return value.NewTernary(t), nil
}

func (f *Filter) evalCursorAttribute(expr parser.CursorAttrebute) (value.Primary, error) {
	var i int
	var err error

	switch expr.Attrebute.Token {
	case parser.COUNT:
		i, err = f.cursors.Count(expr.Cursor)
		if err != nil {
			return nil, err
		}
	}
	return value.NewInteger(int64(i)), nil
}

func (f *Filter) evalPlaceholder(ctx context.Context, expr parser.Placeholder) (value.Primary, error) {
	v := ctx.Value(StatementReplaceValuesContextKey)
	if v == nil {
		return nil, NewStatementReplaceValueNotSpecifiedError(expr)
	}
	replace := v.(*ReplaceValues)

	var idx int
	if 0 < len(expr.Name) {
		i, ok := replace.Names[expr.Name]
		if !ok {
			return nil, NewStatementReplaceValueNotSpecifiedError(expr)
		}
		idx = i
	} else {
		idx = expr.Ordinal - 1
		if len(replace.Values) <= idx {
			return nil, NewStatementReplaceValueNotSpecifiedError(expr)
		}
	}
	return f.Evaluate(ctx, replace.Values[idx])
}

/*
 * Returns single or multiple fields, single record
 */
func (f *Filter) evalRowValue(ctx context.Context, expr parser.QueryExpression) (value.RowValue, error) {
	var rowValue value.RowValue
	var err error

	switch expr.(type) {
	case parser.Subquery:
		rowValue, err = f.evalSubqueryForRowValue(ctx, expr.(parser.Subquery))
	case parser.JsonQuery:
		rowValue, err = f.evalJsonQueryForRowValue(ctx, expr.(parser.JsonQuery))
	case parser.ValueList:
		rowValue, err = f.evalValueList(ctx, expr.(parser.ValueList))
	case parser.RowValue:
		rowValue, err = f.evalRowValue(ctx, expr.(parser.RowValue).Value)
	default:
		p, e := f.Evaluate(ctx, expr)
		if e != nil {
			return rowValue, e
		}
		rowValue = value.RowValue{p}
	}
	return rowValue, err
}

/*
 * Returns multiple fields, multiple records
 */
func (f *Filter) evalRowValueList(ctx context.Context, expr parser.QueryExpression) ([]value.RowValue, error) {
	var list []value.RowValue
	var err error

	switch expr.(type) {
	case parser.Subquery:
		list, err = f.evalSubqueryForRowValueList(ctx, expr.(parser.Subquery))
	case parser.JsonQuery:
		list, err = f.evalJsonQueryForRowValueList(ctx, expr.(parser.JsonQuery))
	case parser.RowValueList:
		rowValueList := expr.(parser.RowValueList)
		list = make([]value.RowValue, len(rowValueList.RowValues))
		for i, v := range rowValueList.RowValues {
			rowValue, e := f.evalRowValue(ctx, v.(parser.RowValue))
			if e != nil {
				return list, e
			}
			list[i] = rowValue
		}
	}

	return list, err
}

/*
 * Returns single fields, multiple records
 */
func (f *Filter) evalArray(ctx context.Context, expr parser.QueryExpression) ([]value.RowValue, error) {
	var array []value.RowValue
	var err error

	switch expr.(type) {
	case parser.Subquery:
		array, err = f.evalSubqueryForArray(ctx, expr.(parser.Subquery))
	case parser.JsonQuery:
		array, err = f.evalJsonQueryForArray(ctx, expr.(parser.JsonQuery))
	case parser.ValueList:
		values, e := f.evalValueList(ctx, expr.(parser.ValueList))
		if e != nil {
			return array, e
		}
		array = make([]value.RowValue, len(values))
		for i, v := range values {
			array[i] = value.RowValue{v}
		}
	case parser.RowValue:
		array, err = f.evalArray(ctx, expr.(parser.RowValue).Value)
	}

	return array, err
}

func (f *Filter) evalSubqueryForRowValue(ctx context.Context, expr parser.Subquery) (value.RowValue, error) {
	view, err := Select(ctx, f, expr.Query)
	if err != nil {
		return nil, err
	}

	if view.RecordLen() < 1 {
		return nil, nil
	}

	if 1 < view.RecordLen() {
		return nil, NewSubqueryTooManyRecordsError(expr)
	}

	rowValue := make(value.RowValue, view.FieldLen())
	for i, cell := range view.RecordSet[0] {
		rowValue[i] = cell.Value()
	}

	return rowValue, nil
}

func (f *Filter) evalJsonQueryForRowValue(ctx context.Context, expr parser.JsonQuery) (value.RowValue, error) {
	query, jsonText, err := f.evalJsonQueryParameters(ctx, expr)
	if err != nil {
		return nil, err
	}

	if value.IsNull(query) || value.IsNull(jsonText) {
		return nil, nil
	}

	_, values, _, err := json.LoadTable(query.(value.String).Raw(), jsonText.(value.String).Raw())
	if err != nil {
		return nil, NewLoadJsonError(expr, err.Error())
	}

	if len(values) < 1 {
		return nil, nil
	}

	if 1 < len(values) {
		return nil, NewJsonQueryTooManyRecordsError(expr)
	}

	rowValue := make(value.RowValue, len(values[0]))
	for i, cell := range values[0] {
		rowValue[i] = cell
	}

	return rowValue, nil
}

func (f *Filter) evalValueList(ctx context.Context, expr parser.ValueList) (value.RowValue, error) {
	values := make(value.RowValue, len(expr.Values))
	for i, v := range expr.Values {
		val, err := f.Evaluate(ctx, v)
		if err != nil {
			return nil, err
		}
		values[i] = val
	}
	return values, nil
}

func (f *Filter) evalSubqueryForRowValueList(ctx context.Context, expr parser.Subquery) ([]value.RowValue, error) {
	view, err := Select(ctx, f, expr.Query)
	if err != nil {
		return nil, err
	}

	if view.RecordLen() < 1 {
		return nil, nil
	}

	list := make([]value.RowValue, view.RecordLen())
	for i, r := range view.RecordSet {
		rowValue := make(value.RowValue, view.FieldLen())
		for j, cell := range r {
			rowValue[j] = cell.Value()
		}
		list[i] = rowValue
	}

	return list, nil
}

func (f *Filter) evalJsonQueryForRowValueList(ctx context.Context, expr parser.JsonQuery) ([]value.RowValue, error) {
	query, jsonText, err := f.evalJsonQueryParameters(ctx, expr)
	if err != nil {
		return nil, err
	}

	if value.IsNull(query) || value.IsNull(jsonText) {
		return nil, nil
	}

	_, values, _, err := json.LoadTable(query.(value.String).Raw(), jsonText.(value.String).Raw())
	if err != nil {
		return nil, NewLoadJsonError(expr, err.Error())
	}

	if len(values) < 1 {
		return nil, nil
	}

	list := make([]value.RowValue, len(values))
	for i, row := range values {
		list[i] = value.RowValue(row)
	}

	return list, nil
}

func (f *Filter) evalSubqueryForArray(ctx context.Context, expr parser.Subquery) ([]value.RowValue, error) {
	view, err := Select(ctx, f, expr.Query)
	if err != nil {
		return nil, err
	}

	if 1 < view.FieldLen() {
		return nil, NewSubqueryTooManyFieldsError(expr)
	}

	if view.RecordLen() < 1 {
		return nil, nil
	}

	list := make([]value.RowValue, view.RecordLen())
	for i, r := range view.RecordSet {
		list[i] = value.RowValue{r[0].Value()}
	}

	return list, nil
}

func (f *Filter) evalJsonQueryForArray(ctx context.Context, expr parser.JsonQuery) ([]value.RowValue, error) {
	query, jsonText, err := f.evalJsonQueryParameters(ctx, expr)
	if err != nil {
		return nil, err
	}

	if value.IsNull(query) || value.IsNull(jsonText) {
		return nil, nil
	}

	values, err := json.LoadArray(query.(value.String).Raw(), jsonText.(value.String).Raw())
	if err != nil {
		return nil, NewLoadJsonError(expr, err.Error())
	}

	if len(values) < 1 {
		return nil, nil
	}

	list := make([]value.RowValue, len(values))
	for i, v := range values {
		list[i] = value.RowValue{v}
	}

	return list, nil
}

func (f *Filter) evalJsonQueryParameters(ctx context.Context, expr parser.JsonQuery) (value.Primary, value.Primary, error) {
	queryValue, err := f.Evaluate(ctx, expr.Query)
	if err != nil {
		return nil, nil, err
	}
	query := value.ToString(queryValue)

	jsonTextValue, err := f.Evaluate(ctx, expr.JsonText)
	if err != nil {
		return nil, nil, err
	}
	jsonText := value.ToString(jsonTextValue)

	return query, jsonText, nil
}

func (f *Filter) EvaluateEmbeddedString(ctx context.Context, embedded string) (string, error) {
	scanner := new(excmd.ArgumentScanner).Init(embedded)
	buf := new(bytes.Buffer)
	var err error

	for scanner.Scan() {
		switch scanner.ElementType() {
		case excmd.FixedString:
			buf.WriteString(scanner.Text())
		case excmd.Variable:
			if err = f.writeEmbeddedExpression(ctx, buf, parser.Variable{Name: scanner.Text()}); err != nil {
				return buf.String(), err
			}
		case excmd.EnvironmentVariable:
			buf.WriteString(os.Getenv(scanner.Text()))
		case excmd.RuntimeInformation:
			if err = f.writeEmbeddedExpression(ctx, buf, parser.RuntimeInformation{Name: scanner.Text()}); err != nil {
				return buf.String(), err
			}
		case excmd.CsvqExpression:
			expr := scanner.Text()
			if 0 < len(expr) {
				statements, _, err := parser.Parse(expr, "", f.tx.Flags.DatetimeFormat, false)
				if err != nil {
					if syntaxErr, ok := err.(*parser.SyntaxError); ok {
						err = NewSyntaxError(syntaxErr)
					}
					return buf.String(), err
				}

				switch len(statements) {
				case 1:
					qexpr, ok := statements[0].(parser.QueryExpression)
					if !ok {
						return buf.String(), NewInvalidValueExpressionError(parser.NewStringValue(expr))
					}
					if err = f.writeEmbeddedExpression(ctx, buf, qexpr); err != nil {
						return buf.String(), err
					}
				default:
					return buf.String(), NewInvalidValueExpressionError(parser.NewStringValue(expr))
				}
			}
		}
	}
	if err = scanner.Err(); err != nil {
		return buf.String(), err
	}

	return buf.String(), nil
}

func (f *Filter) writeEmbeddedExpression(ctx context.Context, buf *bytes.Buffer, expr parser.QueryExpression) error {
	p, err := f.Evaluate(ctx, expr)
	if err != nil {
		return err
	}
	s, _ := NewStringFormatter().Format("%s", []value.Primary{p})
	buf.WriteString(s)
	return nil
}
