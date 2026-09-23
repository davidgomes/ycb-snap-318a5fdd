package tengo

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"

	"github.com/d5/tengo/v2/parser"
)

// Script can simplify compilation and execution of embedded scripts.
type Script struct {
	variables        map[string]*Variable
	modules          ModuleGetter
	input            []byte
	maxAllocs        int64
	maxConstObjects  int
	enableFileImport bool
	importDir        string
}

// NewScript creates a Script instance with an input script.
func NewScript(input []byte) *Script {
	return &Script{
		variables:       make(map[string]*Variable),
		input:           input,
		maxAllocs:       -1,
		maxConstObjects: -1,
	}
}

// Add adds a new variable or updates an existing variable to the script.
func (s *Script) Add(name string, value interface{}) error {
	obj, err := FromInterface(value)
	if err != nil {
		return err
	}
	s.variables[name] = &Variable{
		name:  name,
		value: obj,
	}
	return nil
}

// Remove removes (undefines) an existing variable for the script. It returns
// false if the variable name is not defined.
func (s *Script) Remove(name string) bool {
	if _, ok := s.variables[name]; !ok {
		return false
	}
	delete(s.variables, name)
	return true
}

// SetImports sets import modules.
func (s *Script) SetImports(modules ModuleGetter) {
	s.modules = modules
}

// SetImportDir sets the initial import directory for script files.
func (s *Script) SetImportDir(dir string) error {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	s.importDir = dir
	return nil
}

// SetMaxAllocs sets the maximum number of objects allocations during the run
// time. Compiled script will return ErrObjectAllocLimit error if it
// exceeds this limit.
func (s *Script) SetMaxAllocs(n int64) {
	s.maxAllocs = n
}

// SetMaxConstObjects sets the maximum number of objects in the compiled
// constants.
func (s *Script) SetMaxConstObjects(n int) {
	s.maxConstObjects = n
}

// EnableFileImport enables or disables module loading from local files. Local
// file modules are disabled by default.
func (s *Script) EnableFileImport(enable bool) {
	s.enableFileImport = enable
}

// Compile compiles the script with all the defined variables, and, returns
// Compiled object.
func (s *Script) Compile() (*Compiled, error) {
	symbolTable, globals, err := s.prepCompile()
	if err != nil {
		return nil, err
	}

	fileSet := parser.NewFileSet()
	srcFile := fileSet.AddFile("(main)", -1, len(s.input))
	p := parser.NewParser(srcFile, s.input, nil)
	file, err := p.ParseFile()
	if err != nil {
		return nil, err
	}

	c := NewCompiler(srcFile, symbolTable, nil, s.modules, nil)
	c.EnableFileImport(s.enableFileImport)
	c.SetImportDir(s.importDir)
	if err := c.Compile(file); err != nil {
		return nil, err
	}

	// reduce globals size
	globals = globals[:symbolTable.MaxSymbols()+1]

	// global symbol names to indexes
	globalIndexes := make(map[string]int, len(globals))
	for _, name := range symbolTable.Names() {
		symbol, _, _ := symbolTable.Resolve(name, false)
		if symbol.Scope == ScopeGlobal {
			globalIndexes[name] = symbol.Index
		}
	}

	// remove duplicates from constants
	bytecode := c.Bytecode()
	bytecode.RemoveDuplicates()

	// check the constant objects limit
	if s.maxConstObjects >= 0 {
		cnt := bytecode.CountObjects()
		if cnt > s.maxConstObjects {
			return nil, fmt.Errorf("exceeding constant objects limit: %d", cnt)
		}
	}
	compiled := &Compiled{
		globalIndexes: globalIndexes,
		bytecode:      bytecode,
		globals:       globals,
		maxAllocs:     s.maxAllocs,
	}
	compiled.rt = newRuntime(bytecode, globals, s.maxAllocs)
	for idx, g := range globals {
		if g != nil {
			globals[idx] = compiled.importObject(g)
		}
	}
	return compiled, nil
}

// Run compiles and runs the scripts. Use returned compiled object to access
// global variables.
func (s *Script) Run() (compiled *Compiled, err error) {
	compiled, err = s.Compile()
	if err != nil {
		return
	}
	err = compiled.Run()
	return
}

// RunContext is like Run but includes a context.
func (s *Script) RunContext(
	ctx context.Context,
) (compiled *Compiled, err error) {
	compiled, err = s.Compile()
	if err != nil {
		return
	}
	err = compiled.RunContext(ctx)
	return
}

func (s *Script) prepCompile() (
	symbolTable *SymbolTable,
	globals []Object,
	err error,
) {
	var names []string
	for name := range s.variables {
		names = append(names, name)
	}

	symbolTable = NewSymbolTable()
	for idx, fn := range builtinFuncs {
		symbolTable.DefineBuiltin(idx, fn.Name)
	}

	globals = make([]Object, GlobalsSize)

	for idx, name := range names {
		symbol := symbolTable.Define(name)
		if symbol.Index != idx {
			panic(fmt.Errorf("wrong symbol index: %d != %d",
				idx, symbol.Index))
		}
		globals[symbol.Index] = s.variables[name].value
	}
	return
}

// Compiled is a compiled instance of the user script. Use Script.Compile() to
// create Compiled object.
type Compiled struct {
	globalIndexes map[string]int // global symbol name to index
	bytecode      *Bytecode
	globals       []Object
	maxAllocs     int64
	lock          sync.RWMutex

	// rt binds functions created by this instance to its globals.
	rt *vmRuntime
	// foreignRuntimes bind functions transferred from instances with a
	// different bytecode to this instance's globals, keyed by that bytecode.
	foreignRuntimes map[*Bytecode]*vmRuntime
}

// Run executes the compiled script in the virtual machine.
func (c *Compiled) Run() error {
	c.lock.Lock()
	defer c.lock.Unlock()

	v := newVM(c.rt, c.bytecode.MainFunction)
	return v.Run()
}

// RunContext is like Run but includes a context.
func (c *Compiled) RunContext(ctx context.Context) (err error) {
	c.lock.Lock()
	defer c.lock.Unlock()

	v := newVM(c.rt, c.bytecode.MainFunction)
	ch := make(chan error, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				switch e := r.(type) {
				case string:
					ch <- fmt.Errorf(e)
				case error:
					ch <- e
				default:
					ch <- fmt.Errorf("unknown panic: %v", e)
				}
			}
		}()
		ch <- v.Run()
	}()

	select {
	case <-ctx.Done():
		v.Abort()
		<-ch
		err = ctx.Err()
	case err = <-ch:
	}
	return
}

// Size of compiled script in bytes
// (as much as we can calculate it without reflection and black magic)
func (c *Compiled) Size() int64 {
	c.lock.RLock()
	defer c.lock.RUnlock()

	return c.bytecode.Size() + int64(len(c.globalIndexes)+len(c.globals))
}

// Clone creates a new copy of Compiled. Cloned copies are safe for concurrent
// use by multiple goroutines.
func (c *Compiled) Clone() *Compiled {
	c.lock.RLock()
	defer c.lock.RUnlock()

	clone := &Compiled{
		globalIndexes: c.globalIndexes,
		bytecode:      c.bytecode,
		globals:       make([]Object, len(c.globals)),
		maxAllocs:     c.maxAllocs,
	}
	clone.rt = newRuntime(c.bytecode, clone.globals, c.maxAllocs)
	// copy global objects
	t := newTransfer(clone)
	for idx, g := range c.globals {
		if g != nil {
			clone.globals[idx] = t.object(g)
		}
	}
	return clone
}

// runtimeFor returns the runtime that binds functions compiled into bytecode
// to the globals of c.
func (c *Compiled) runtimeFor(bytecode *Bytecode) *vmRuntime {
	if bytecode == c.bytecode {
		return c.rt
	}
	if rt, ok := c.foreignRuntimes[bytecode]; ok {
		return rt
	}
	if c.foreignRuntimes == nil {
		c.foreignRuntimes = make(map[*Bytecode]*vmRuntime)
	}
	rt := newRuntime(bytecode, c.globals, c.maxAllocs)
	c.foreignRuntimes[bytecode] = rt
	return rt
}

// importObject prepares a value to be stored in the globals of c. Values that
// reach functions bound to another instance are deep-copied so that those
// functions are rebound to c and their captured variables are snapshotted;
// all other values are stored as they are.
func (c *Compiled) importObject(o Object) Object {
	if !reachesForeignFunction(o, c.globals, make(map[Object]bool)) {
		return o
	}
	return newTransfer(c).object(o)
}

func reachesForeignFunction(
	o Object,
	globals []Object,
	visited map[Object]bool,
) bool {
	switch o.(type) {
	case *CompiledFunction, *Array, *ImmutableArray, *Map, *ImmutableMap,
		*Error:
		if visited[o] {
			return false
		}
		visited[o] = true
	}
	switch o := o.(type) {
	case *CompiledFunction:
		if o.rt != nil && !o.rt.sharesGlobals(globals) {
			return true
		}
		for _, p := range o.Free {
			if p != nil && p.Value != nil &&
				reachesForeignFunction(*p.Value, globals, visited) {
				return true
			}
		}
	case *Array:
		for _, e := range o.Value {
			if reachesForeignFunction(e, globals, visited) {
				return true
			}
		}
	case *ImmutableArray:
		for _, e := range o.Value {
			if reachesForeignFunction(e, globals, visited) {
				return true
			}
		}
	case *Map:
		for _, e := range o.Value {
			if reachesForeignFunction(e, globals, visited) {
				return true
			}
		}
	case *ImmutableMap:
		for _, e := range o.Value {
			if reachesForeignFunction(e, globals, visited) {
				return true
			}
		}
	case *Error:
		return reachesForeignFunction(o.Value, globals, visited)
	}
	return false
}

// transfer deep-copies values into a destination Compiled. Functions bound to
// another instance are rebound to the destination globals, and their captured
// variables are copied as they are at transfer time. Shared captures, shared
// containers and cycles are preserved within a single transfer.
type transfer struct {
	dst    *Compiled
	copies map[Object]Object
}

func newTransfer(dst *Compiled) *transfer {
	return &transfer{dst: dst, copies: make(map[Object]Object)}
}

func (t *transfer) object(o Object) Object {
	if o == nil {
		return nil
	}
	switch o.(type) {
	case *CompiledFunction, *Array, *ImmutableArray, *Map, *ImmutableMap,
		*Error:
		if c, ok := t.copies[o]; ok {
			return c
		}
	}
	switch o := o.(type) {
	case *CompiledFunction:
		if o.rt == nil {
			return o.Copy()
		}
		if o.rt.sharesGlobals(t.dst.globals) {
			return o
		}
		fn := &CompiledFunction{
			Instructions:  o.Instructions,
			NumLocals:     o.NumLocals,
			NumParameters: o.NumParameters,
			VarArgs:       o.VarArgs,
			SourceMap:     o.SourceMap,
			Free:          make([]*ObjectPtr, len(o.Free)),
			rt:            t.dst.runtimeFor(o.rt.bytecode),
		}
		t.copies[o] = fn
		for i, p := range o.Free {
			fn.Free[i] = t.freeVar(p)
		}
		return fn
	case *Array:
		arr := &Array{Value: make([]Object, len(o.Value))}
		t.copies[o] = arr
		for i, e := range o.Value {
			arr.Value[i] = t.object(e)
		}
		return arr
	case *ImmutableArray:
		arr := &ImmutableArray{Value: make([]Object, len(o.Value))}
		t.copies[o] = arr
		for i, e := range o.Value {
			arr.Value[i] = t.object(e)
		}
		return arr
	case *Map:
		m := &Map{Value: make(map[string]Object, len(o.Value))}
		t.copies[o] = m
		for k, e := range o.Value {
			m.Value[k] = t.object(e)
		}
		return m
	case *ImmutableMap:
		m := &ImmutableMap{Value: make(map[string]Object, len(o.Value))}
		t.copies[o] = m
		for k, e := range o.Value {
			m.Value[k] = t.object(e)
		}
		return m
	case *Error:
		e := &Error{}
		t.copies[o] = e
		e.Value = t.object(o.Value)
		return e
	default:
		return o.Copy()
	}
}

func (t *transfer) freeVar(p *ObjectPtr) *ObjectPtr {
	if p == nil {
		return nil
	}
	if c, ok := t.copies[p]; ok {
		return c.(*ObjectPtr)
	}
	var val Object
	np := &ObjectPtr{Value: &val}
	t.copies[p] = np
	if p.Value != nil {
		val = t.object(*p.Value)
	}
	return np
}

// IsDefined returns true if the variable name is defined (has value) before or
// after the execution.
func (c *Compiled) IsDefined(name string) bool {
	c.lock.RLock()
	defer c.lock.RUnlock()

	idx, ok := c.globalIndexes[name]
	if !ok {
		return false
	}
	v := c.globals[idx]
	if v == nil {
		return false
	}
	return v != UndefinedValue
}

// Get returns a variable identified by the name.
func (c *Compiled) Get(name string) *Variable {
	c.lock.RLock()
	defer c.lock.RUnlock()

	value := UndefinedValue
	if idx, ok := c.globalIndexes[name]; ok {
		value = c.globals[idx]
		if value == nil {
			value = UndefinedValue
		}
	}
	return &Variable{
		name:  name,
		value: value,
	}
}

// GetAll returns all the variables that are defined by the compiled script.
func (c *Compiled) GetAll() []*Variable {
	c.lock.RLock()
	defer c.lock.RUnlock()

	var vars []*Variable
	for name, idx := range c.globalIndexes {
		value := c.globals[idx]
		if value == nil {
			value = UndefinedValue
		}
		vars = append(vars, &Variable{
			name:  name,
			value: value,
		})
	}
	return vars
}

// Set replaces the value of a global variable identified by the name. An error
// will be returned if the name was not defined during compilation.
func (c *Compiled) Set(name string, value interface{}) error {
	c.lock.Lock()
	defer c.lock.Unlock()

	obj, err := FromInterface(value)
	if err != nil {
		return err
	}
	idx, ok := c.globalIndexes[name]
	if !ok {
		return fmt.Errorf("'%s' is not defined", name)
	}
	c.globals[idx] = c.importObject(obj)
	return nil
}
