package functions

import (
	"fmt"
	"math"

	"github.com/influxdata/platform/query"
	"github.com/influxdata/platform/query/execute"
	"github.com/influxdata/platform/query/plan"
	"github.com/influxdata/platform/query/semantic"
	"github.com/influxdata/platform/query/values"
	"github.com/pkg/errors"
)

const WindowKind = "window"

type WindowOpSpec struct {
	Every         query.Duration    `json:"every"`
	Period        query.Duration    `json:"period"`
	Start         query.Time        `json:"start"`
	Round         query.Duration    `json:"round"`
	Triggering    query.TriggerSpec `json:"triggering"`
	TimeCol       string            `json:"time_col"`
	StopColLabel  string            `json:"stop_col_label"`
	StartColLabel string            `json:"start_col_label"`
	CreateEmpty   bool              `json:"createEmpty"`
}

var infinityVar = values.NewDurationValue(math.MaxInt64)

var windowSignature = query.DefaultFunctionSignature()

func init() {
	windowSignature.Params["every"] = semantic.Duration
	windowSignature.Params["period"] = semantic.Duration
	windowSignature.Params["round"] = semantic.Duration
	windowSignature.Params["start"] = semantic.Time
	windowSignature.Params["timeCol"] = semantic.String
	windowSignature.Params["startColLabel"] = semantic.String
	windowSignature.Params["stopColLabel"] = semantic.String
	windowSignature.Params["createEmpty"] = semantic.Bool

	query.RegisterFunction(WindowKind, createWindowOpSpec, windowSignature)
	query.RegisterOpSpec(WindowKind, newWindowOp)
	query.RegisterBuiltInValue("inf", infinityVar)
	plan.RegisterProcedureSpec(WindowKind, newWindowProcedure, WindowKind)
	execute.RegisterTransformation(WindowKind, createWindowTransformation)
}

func createWindowOpSpec(args query.Arguments, a *query.Administration) (query.OperationSpec, error) {
	if err := a.AddParentFromArgs(args); err != nil {
		return nil, err
	}

	spec := new(WindowOpSpec)
	every, everySet, err := args.GetDuration("every")
	if err != nil {
		return nil, err
	}
	if everySet {
		spec.Every = query.Duration(every)
	}
	period, periodSet, err := args.GetDuration("period")
	if err != nil {
		return nil, err
	}
	if periodSet {
		spec.Period = period
	}
	if round, ok, err := args.GetDuration("round"); err != nil {
		return nil, err
	} else if ok {
		spec.Round = round
	}
	if start, ok, err := args.GetTime("start"); err != nil {
		return nil, err
	} else if ok {
		spec.Start = start
	}

	if !everySet && !periodSet {
		return nil, errors.New(`window function requires at least one of "every" or "period" to be set`)
	}

	if label, ok, err := args.GetString("timeCol"); err != nil {
		return nil, err
	} else if ok {
		spec.TimeCol = label
	} else {
		spec.TimeCol = execute.DefaultTimeColLabel
	}
	if label, ok, err := args.GetString("startColLabel"); err != nil {
		return nil, err
	} else if ok {
		spec.StartColLabel = label
	} else {
		spec.StartColLabel = execute.DefaultStartColLabel
	}
	if label, ok, err := args.GetString("stopColLabel"); err != nil {
		return nil, err
	} else if ok {
		spec.StopColLabel = label
	} else {
		spec.StopColLabel = execute.DefaultStopColLabel
	}
	if createEmpty, ok, err := args.GetBool("createEmpty"); err != nil {
		return nil, err
	} else if ok {
		spec.CreateEmpty = createEmpty
	} else {
		spec.CreateEmpty = false
	}

	// Apply defaults
	if !everySet {
		spec.Every = spec.Period
	}
	if !periodSet {
		spec.Period = spec.Every
	}
	return spec, nil
}

func newWindowOp() query.OperationSpec {
	return new(WindowOpSpec)
}

func (s *WindowOpSpec) Kind() query.OperationKind {
	return WindowKind
}

type WindowProcedureSpec struct {
	Window     plan.WindowSpec
	Triggering query.TriggerSpec
	TimeCol,
	StartColLabel,
	StopColLabel string
	CreateEmpty bool
}

func newWindowProcedure(qs query.OperationSpec, pa plan.Administration) (plan.ProcedureSpec, error) {
	s, ok := qs.(*WindowOpSpec)
	if !ok {
		return nil, fmt.Errorf("invalid spec type %T", qs)
	}
	p := &WindowProcedureSpec{
		Window: plan.WindowSpec{
			Every:  s.Every,
			Period: s.Period,
			Round:  s.Round,
			Start:  s.Start,
		},
		Triggering:    s.Triggering,
		TimeCol:       s.TimeCol,
		StartColLabel: s.StartColLabel,
		StopColLabel:  s.StopColLabel,
		CreateEmpty:   s.CreateEmpty,
	}
	if p.Triggering == nil {
		p.Triggering = query.DefaultTrigger
	}
	return p, nil
}

func (s *WindowProcedureSpec) Kind() plan.ProcedureKind {
	return WindowKind
}
func (s *WindowProcedureSpec) Copy() plan.ProcedureSpec {
	ns := new(WindowProcedureSpec)
	ns.Window = s.Window
	ns.Triggering = s.Triggering
	return ns
}

func (s *WindowProcedureSpec) TriggerSpec() query.TriggerSpec {
	return s.Triggering
}

func createWindowTransformation(id execute.DatasetID, mode execute.AccumulationMode, spec plan.ProcedureSpec, a execute.Administration) (execute.Transformation, execute.Dataset, error) {
	s, ok := spec.(*WindowProcedureSpec)
	if !ok {
		return nil, nil, fmt.Errorf("invalid spec type %T", spec)
	}
	cache := execute.NewTableBuilderCache(a.Allocator())
	d := execute.NewDataset(id, mode, cache)
	var start execute.Time
	if s.Window.Start.IsZero() {
		start = a.ResolveTime(query.Now).Truncate(execute.Duration(s.Window.Every))
	} else {
		start = a.ResolveTime(s.Window.Start)
	}

	bounds := a.StreamContext().Bounds()
	if bounds == nil {
		return nil, nil, errors.New("nil bounds passed to window")
	}

	t := NewFixedWindowTransformation(
		d,
		cache,
		*bounds,
		execute.Window{
			Every:  execute.Duration(s.Window.Every),
			Period: execute.Duration(s.Window.Period),
			Round:  execute.Duration(s.Window.Round),
			Start:  start,
		},
		s.TimeCol,
		s.StartColLabel,
		s.StopColLabel,
		s.CreateEmpty,
	)
	return t, d, nil
}

type fixedWindowTransformation struct {
	d         execute.Dataset
	cache     execute.TableBuilderCache
	w         execute.Window
	bounds    execute.Bounds
	allBounds []execute.Bounds

	offset execute.Duration

	timeCol,
	startColLabel,
	stopColLabel string
	createEmpty bool
}

func NewFixedWindowTransformation(
	d execute.Dataset,
	cache execute.TableBuilderCache,
	bounds execute.Bounds,
	w execute.Window,
	timeCol,
	startColLabel,
	stopColLabel string,
	createEmpty bool,
) execute.Transformation {
	offset := execute.Duration(w.Start - w.Start.Truncate(w.Every))
	t := &fixedWindowTransformation{
		d:             d,
		cache:         cache,
		w:             w,
		bounds:        bounds,
		offset:        offset,
		timeCol:       timeCol,
		startColLabel: startColLabel,
		stopColLabel:  stopColLabel,
		createEmpty:   createEmpty,
	}

	if createEmpty {
		t.generateWindowsWithinBounds()
	}

	return t
}

func (t *fixedWindowTransformation) RetractTable(id execute.DatasetID, key query.GroupKey) (err error) {
	panic("not implemented")
}

func (t *fixedWindowTransformation) Process(id execute.DatasetID, tbl query.Table) error {
	timeIdx := execute.ColIdx(t.timeCol, tbl.Cols())

	newCols := make([]query.ColMeta, 0, len(tbl.Cols())+2)
	keyCols := make([]query.ColMeta, 0, len(tbl.Cols())+2)
	keyColMap := make([]int, 0, len(tbl.Cols())+2)
	startColIdx := -1
	stopColIdx := -1
	for j, c := range tbl.Cols() {
		keyIdx := execute.ColIdx(c.Label, tbl.Key().Cols())
		keyed := keyIdx >= 0
		if c.Label == t.startColLabel {
			startColIdx = j
			keyed = true
		}
		if c.Label == t.stopColLabel {
			stopColIdx = j
			keyed = true
		}
		newCols = append(newCols, c)
		if keyed {
			keyCols = append(keyCols, c)
			keyColMap = append(keyColMap, keyIdx)
		}
	}
	if startColIdx == -1 {
		startColIdx = len(newCols)
		c := query.ColMeta{
			Label: t.startColLabel,
			Type:  query.TTime,
		}
		newCols = append(newCols, c)
		keyCols = append(keyCols, c)
		keyColMap = append(keyColMap, len(keyColMap))
	}
	if stopColIdx == -1 {
		stopColIdx = len(newCols)
		c := query.ColMeta{
			Label: t.stopColLabel,
			Type:  query.TTime,
		}
		newCols = append(newCols, c)
		keyCols = append(keyCols, c)
		keyColMap = append(keyColMap, len(keyColMap))
	}

	// Abort processing if no data will match bounds
	if t.bounds.IsEmpty() {
		return nil
	}

	for _, bnds := range t.allBounds {
		key := t.newWindowGroupKey(tbl, keyCols, bnds, keyColMap)
		builder, created := t.cache.TableBuilder(key)
		if created {
			for _, c := range newCols {
				builder.AddCol(c)
			}
		}
	}

	return tbl.Do(func(cr query.ColReader) error {
		l := cr.Len()
		for i := 0; i < l; i++ {
			tm := cr.Times(timeIdx)[i]
			bounds := t.getWindowBounds(tm)

			for _, bnds := range bounds {
				key := t.newWindowGroupKey(tbl, keyCols, bnds, keyColMap)
				builder, created := t.cache.TableBuilder(key)
				if created {
					for _, c := range newCols {
						builder.AddCol(c)
					}
				}

				for j, c := range builder.Cols() {
					switch c.Label {
					case t.startColLabel:
						builder.AppendTime(startColIdx, bnds.Start)
					case t.stopColLabel:
						builder.AppendTime(stopColIdx, bnds.Stop)
					default:
						switch c.Type {
						case query.TBool:
							builder.AppendBool(j, cr.Bools(j)[i])
						case query.TInt:
							builder.AppendInt(j, cr.Ints(j)[i])
						case query.TUInt:
							builder.AppendUInt(j, cr.UInts(j)[i])
						case query.TFloat:
							builder.AppendFloat(j, cr.Floats(j)[i])
						case query.TString:
							builder.AppendString(j, cr.Strings(j)[i])
						case query.TTime:
							builder.AppendTime(j, cr.Times(j)[i])
						default:
							execute.PanicUnknownType(c.Type)
						}
					}
				}
			}
		}
		return nil
	})
}

func (t *fixedWindowTransformation) newWindowGroupKey(tbl query.Table, keyCols []query.ColMeta, bnds execute.Bounds, keyColMap []int) query.GroupKey {
	cols := make([]query.ColMeta, len(keyCols))
	vs := make([]values.Value, len(keyCols))
	for j, c := range keyCols {
		cols[j] = c
		switch c.Label {
		case t.startColLabel:
			vs[j] = values.NewTimeValue(bnds.Start)
		case t.stopColLabel:
			vs[j] = values.NewTimeValue(bnds.Stop)
		default:
			vs[j] = tbl.Key().Value(keyColMap[j])
		}
	}
	return execute.NewGroupKey(cols, vs)
}

func (t *fixedWindowTransformation) generateInitialBounds(boundsStart, boundsStop execute.Time) (execute.Time, execute.Time) {
	stop := boundsStart.Truncate(t.w.Every) + execute.Time(t.offset)
	if boundsStop >= stop {
		stop += execute.Time(t.w.Every)
	}
	start := stop - execute.Time(t.w.Period)

	return start, stop
}

func (t *fixedWindowTransformation) clipBounds(bnds *execute.Bounds) {
	// Check against procedure bounds
	if bnds.Stop > t.bounds.Stop {
		bnds.Stop = t.bounds.Stop
	}

	if bnds.Start < t.bounds.Start {
		bnds.Start = t.bounds.Start
	}
}

func (t *fixedWindowTransformation) getWindowBounds(now execute.Time) []execute.Bounds {
	if t.w.Every == infinityVar.Duration() {
		return []execute.Bounds{
			{Start: execute.MinTime, Stop: execute.MaxTime},
		}
	}
	start, stop := t.generateInitialBounds(now, now)

	var bounds []execute.Bounds

	for now >= start {
		bnds := execute.Bounds{
			Start: start,
			Stop:  stop,
		}

		t.clipBounds(&bnds)
		bounds = append(bounds, bnds)

		stop += execute.Time(t.w.Every)
		start += execute.Time(t.w.Every)
	}

	return bounds
}

func (t *fixedWindowTransformation) generateWindowsWithinBounds() {
	if t.w.Every == infinityVar.Duration() {
		t.allBounds = []execute.Bounds{
			{Start: execute.MinTime, Stop: execute.MaxTime},
		}
		return
	}
	start, stop := t.generateInitialBounds(t.bounds.Start, t.bounds.Stop)

	var bounds []execute.Bounds

	for t.bounds.Stop > start {
		bnds := execute.Bounds{
			Start: start,
			Stop:  stop,
		}

		t.clipBounds(&bnds)
		bounds = append(bounds, bnds)

		start += execute.Time(t.w.Every)
		stop += execute.Time(t.w.Every)
	}
	t.allBounds = bounds
}

func (t *fixedWindowTransformation) UpdateWatermark(id execute.DatasetID, mark execute.Time) error {
	return t.d.UpdateWatermark(mark)
}
func (t *fixedWindowTransformation) UpdateProcessingTime(id execute.DatasetID, pt execute.Time) error {
	return t.d.UpdateProcessingTime(pt)
}
func (t *fixedWindowTransformation) Finish(id execute.DatasetID, err error) {
	t.d.Finish(err)
}
