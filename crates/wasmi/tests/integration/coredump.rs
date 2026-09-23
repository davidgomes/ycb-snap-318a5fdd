//! Tests for Wasm coredump generation upon Wasm traps.

use wasmi::{
    Caller,
    CompilationMode,
    Config,
    Engine,
    Error,
    Extern,
    Func,
    Instance,
    Linker,
    Module,
    Store,
    TrapCode,
    TypedResumableCall,
};
use wasmparser::{
    BinaryReader,
    CoreDumpInstance,
    CoreDumpInstancesSection,
    CoreDumpModulesSection,
    CoreDumpSection,
    CoreDumpStackFrame,
    CoreDumpStackSection,
    CoreDumpValue,
    DataKind,
    GlobalType,
    MemoryType,
    Operator,
    Parser,
    Payload,
    ValType,
    Validator,
};

/// A decoded Wasm coredump.
#[derive(Debug)]
struct CoreDump {
    executable: String,
    modules: Vec<String>,
    instances: Vec<CoreDumpInstance>,
    thread: String,
    frames: Vec<CoreDumpStackFrame>,
    memories: Vec<MemoryType>,
    globals: Vec<(GlobalType, GlobalValue)>,
    data: Vec<DataSegment>,
}

/// The value of a global variable in a coredump.
#[derive(Debug, Copy, Clone, PartialEq)]
enum GlobalValue {
    I32(i32),
    I64(i64),
    F32(u32),
    F64(u64),
}

/// An active data segment of a coredump.
#[derive(Debug)]
struct DataSegment {
    memory_index: u32,
    offset: u64,
    data: Vec<u8>,
}

impl CoreDump {
    /// Validates and decodes the coredump `bytes`.
    fn decode(bytes: &[u8]) -> Self {
        Validator::new()
            .validate_all(bytes)
            .expect("coredump must be a valid Wasm binary");
        let mut coredump = Self {
            executable: String::new(),
            modules: Vec::new(),
            instances: Vec::new(),
            thread: String::new(),
            frames: Vec::new(),
            memories: Vec::new(),
            globals: Vec::new(),
            data: Vec::new(),
        };
        let mut custom_sections = Vec::new();
        for payload in Parser::new(0).parse_all(bytes) {
            match payload.unwrap() {
                Payload::CustomSection(section) => {
                    let reader = BinaryReader::new(section.data(), 0);
                    custom_sections.push(section.name());
                    match section.name() {
                        "core" => {
                            coredump.executable = CoreDumpSection::new(reader).unwrap().name.into();
                        }
                        "coremodules" => {
                            let modules = CoreDumpModulesSection::new(reader).unwrap().modules;
                            coredump.modules = modules.into_iter().map(String::from).collect();
                        }
                        "coreinstances" => {
                            coredump.instances =
                                CoreDumpInstancesSection::new(reader).unwrap().instances;
                        }
                        "corestack" => {
                            let stack = CoreDumpStackSection::new(reader).unwrap();
                            coredump.thread = stack.name.into();
                            coredump.frames = stack.frames;
                        }
                        name => panic!("unexpected custom section: {name}"),
                    }
                }
                Payload::MemorySection(reader) => {
                    coredump.memories = reader.into_iter().map(Result::unwrap).collect();
                }
                Payload::GlobalSection(reader) => {
                    for global in reader {
                        let global = global.unwrap();
                        let mut ops = global.init_expr.get_operators_reader();
                        let value = match ops.read().unwrap() {
                            Operator::I32Const { value } => GlobalValue::I32(value),
                            Operator::I64Const { value } => GlobalValue::I64(value),
                            Operator::F32Const { value } => GlobalValue::F32(value.bits()),
                            Operator::F64Const { value } => GlobalValue::F64(value.bits()),
                            op => panic!("unexpected global init operator: {op:?}"),
                        };
                        assert!(matches!(ops.read().unwrap(), Operator::End));
                        coredump.globals.push((global.ty, value));
                    }
                }
                Payload::DataSection(reader) => {
                    for segment in reader {
                        let segment = segment.unwrap();
                        let DataKind::Active {
                            memory_index,
                            offset_expr,
                        } = segment.kind
                        else {
                            panic!("coredump data segments must be active")
                        };
                        let offset = match offset_expr.get_operators_reader().read().unwrap() {
                            Operator::I32Const { value } => u64::from(value as u32),
                            Operator::I64Const { value } => value as u64,
                            op => panic!("unexpected data offset operator: {op:?}"),
                        };
                        coredump.data.push(DataSegment {
                            memory_index,
                            offset,
                            data: segment.data.to_vec(),
                        });
                    }
                }
                Payload::Version { .. } | Payload::End(_) => {}
                payload => panic!("unexpected coredump payload: {payload:?}"),
            }
        }
        custom_sections.sort_unstable();
        assert_eq!(
            custom_sections,
            ["core", "coreinstances", "coremodules", "corestack"]
        );
        coredump
    }

    /// Returns the contents of the linear memory at `memory_index` stored in the coredump.
    fn memory_data(&self, memory_index: u32) -> Vec<u8> {
        let mut contents = Vec::new();
        for segment in &self.data {
            if segment.memory_index != memory_index {
                continue;
            }
            let start = segment.offset as usize;
            let end = start + segment.data.len();
            if contents.len() < end {
                contents.resize(end, 0);
            }
            contents[start..end].copy_from_slice(&segment.data);
        }
        contents
    }

    /// Returns the `(instance index, function index)` pairs of all frames from youngest to oldest.
    fn frame_funcs(&self) -> Vec<(u32, u32)> {
        self.frames
            .iter()
            .map(|frame| (frame.instanceidx, frame.funcidx))
            .collect()
    }

    /// Returns the locals of the frame at `index` counting from the youngest frame.
    fn locals(&self, index: usize) -> &[CoreDumpValue] {
        &self.frames[index].locals
    }
}

/// Asserts that `actual` and `expected` values are identical.
#[track_caller]
fn assert_values(actual: &[CoreDumpValue], expected: &[CoreDumpValue]) {
    let is_eq = actual.len() == expected.len()
        && actual
            .iter()
            .zip(expected)
            .all(|(actual, expected)| match (actual, expected) {
                (CoreDumpValue::Missing, CoreDumpValue::Missing) => true,
                (CoreDumpValue::I32(a), CoreDumpValue::I32(b)) => a == b,
                (CoreDumpValue::I64(a), CoreDumpValue::I64(b)) => a == b,
                (CoreDumpValue::F32(a), CoreDumpValue::F32(b)) => a.to_bits() == b.to_bits(),
                (CoreDumpValue::F64(a), CoreDumpValue::F64(b)) => a.to_bits() == b.to_bits(),
                _ => false,
            });
    assert!(is_eq, "actual: {actual:?}\nexpected: {expected:?}");
}

/// Creates an [`Engine`] that generates coredumps using `mode`.
fn coredump_engine(mode: CompilationMode) -> Engine {
    let mut config = Config::default();
    config.generate_coredump(true).compilation_mode(mode);
    Engine::new(&config)
}

/// Returns the decoded coredump of `error` asserting that it traps with `trap_code`.
#[track_caller]
fn expect_coredump(error: &Error, trap_code: TrapCode) -> CoreDump {
    assert_eq!(error.as_trap_code(), Some(trap_code), "error: {error}");
    let bytes = error.coredump().expect("missing coredump for Wasm trap");
    CoreDump::decode(bytes)
}

const COMPILATION_MODES: [CompilationMode; 3] = [
    CompilationMode::Eager,
    CompilationMode::LazyTranslation,
    CompilationMode::Lazy,
];

const TRAP_WITH_STATE: &str = r#"
    (module $trapping
        (memory (export "memory") 1 2)
        (global $g0 i32 (i32.const 7))
        (global $g1 (mut i64) (i64.const 0))
        (global $g2 (mut f32) (f32.const 0))
        (global $g3 f64 (f64.const 0.5))
        (data (i32.const 16) "hello")
        (func $trap (param $a i32) (param $b i64) (result i32)
            (local $c f32) (local $d f64) (local $e i32)
            (local.set $c (f32.const 1.5))
            (local.set $d (f64.const -2.25))
            (local.set $e (i32.add (local.get $a) (i32.const 1)))
            (global.set $g1 (local.get $b))
            (global.set $g2 (local.get $c))
            (i32.store8 (i32.const 0) (i32.const 42))
            (unreachable)
        )
        (func (export "run") (param $x i32) (result i32)
            (call $trap (local.get $x) (i64.const -5))
        )
    )
"#;

#[test]
fn coredump_disabled_by_default() {
    let engine = Engine::default();
    let mut store = Store::new(&engine, ());
    let module = Module::new(&engine, TRAP_WITH_STATE).unwrap();
    let instance = Instance::new(&mut store, &module, &[]).unwrap();
    let run = instance.get_typed_func::<i32, i32>(&store, "run").unwrap();
    let error = run.call(&mut store, 41).unwrap_err();
    assert_eq!(error.as_trap_code(), Some(TrapCode::UnreachableCodeReached));
    assert!(error.coredump().is_none());
}

#[test]
fn coredump_records_trap_state() {
    for mode in COMPILATION_MODES {
        let engine = coredump_engine(mode);
        let mut store = Store::new(&engine, ());
        let module = Module::new(&engine, TRAP_WITH_STATE).unwrap();
        let instance = Instance::new(&mut store, &module, &[]).unwrap();
        let run = instance.get_typed_func::<i32, i32>(&store, "run").unwrap();
        let error = run.call(&mut store, 41).unwrap_err();
        let coredump = expect_coredump(&error, TrapCode::UnreachableCodeReached);

        assert_eq!(coredump.executable, "");
        assert_eq!(coredump.modules, ["trapping"]);
        assert_eq!(coredump.instances.len(), 1);
        assert_eq!(coredump.instances[0].module_index, 0);
        assert_eq!(coredump.instances[0].memories, [0]);
        assert_eq!(coredump.instances[0].globals, [0, 1, 2, 3]);
        assert_eq!(coredump.thread, "main");

        // Frames are ordered from youngest to oldest.
        assert_eq!(coredump.frame_funcs(), [(0, 0), (0, 1)]);
        assert!(coredump.frames.iter().all(|frame| frame.codeoffset == 0));
        assert!(coredump.frames.iter().all(|frame| frame.stack.is_empty()));
        assert_values(
            coredump.locals(0),
            &[
                CoreDumpValue::I32(41),
                CoreDumpValue::I64(-5),
                CoreDumpValue::F32(1.5),
                CoreDumpValue::F64(-2.25),
                CoreDumpValue::I32(42),
            ],
        );
        assert_values(coredump.locals(1), &[CoreDumpValue::I32(41)]);

        assert_eq!(coredump.memories.len(), 1);
        let memory = coredump.memories[0];
        assert_eq!((memory.initial, memory.maximum), (1, Some(2)));
        assert!(!memory.memory64);
        let data = coredump.memory_data(0);
        assert_eq!(data.len(), 0x1_0000);
        assert_eq!(data[0], 42);
        assert_eq!(&data[16..21], b"hello");
        let memory = instance.get_memory(&store, "memory").unwrap();
        assert_eq!(&data[..], memory.data(&store));

        let globals: Vec<_> = coredump
            .globals
            .iter()
            .map(|(ty, value)| (ty.content_type, ty.mutable, *value))
            .collect();
        assert_eq!(
            globals,
            [
                (ValType::I32, false, GlobalValue::I32(7)),
                (ValType::I64, true, GlobalValue::I64(-5)),
                (ValType::F32, true, GlobalValue::F32(1.5_f32.to_bits())),
                (ValType::F64, false, GlobalValue::F64(0.5_f64.to_bits())),
            ]
        );

        // The coredump is a valid Wasm module that Wasmi can load.
        Module::new(&Engine::default(), error.coredump().unwrap()).unwrap();
    }
}

#[test]
fn coredump_executable_name() {
    let mut config = Config::default();
    config
        .generate_coredump(true)
        .coredump_executable_name("/usr/bin/trapping.wasm");
    let engine = Engine::new(&config);
    let mut store = Store::new(&engine, ());
    let module = Module::new(&engine, "(module (func (export \"run\") unreachable))").unwrap();
    let instance = Instance::new(&mut store, &module, &[]).unwrap();
    let run = instance.get_typed_func::<(), ()>(&store, "run").unwrap();
    let error = run.call(&mut store, ()).unwrap_err();
    let coredump = expect_coredump(&error, TrapCode::UnreachableCodeReached);
    assert_eq!(coredump.executable, "/usr/bin/trapping.wasm");
    // Modules without a name section have an empty name.
    assert_eq!(coredump.modules, [""]);
    assert_eq!(coredump.frame_funcs(), [(0, 0)]);
    assert!(coredump.memories.is_empty());
    assert!(coredump.globals.is_empty());
    assert!(coredump.data.is_empty());
}

#[test]
fn coredump_for_different_traps() {
    let wasm = r#"
        (module
            (memory 1)
            (func (export "div") (param i32 i32) (result i32)
                (i32.div_s (local.get 0) (local.get 1))
            )
            (func (export "load") (param i32) (result i32)
                (i32.load (local.get 0))
            )
            (func $recurse (export "recurse") (param i64)
                (call $recurse (i64.add (local.get 0) (i64.const 1)))
            )
        )
    "#;
    let mut config = Config::default();
    config.generate_coredump(true).set_max_recursion_depth(10);
    let engine = Engine::new(&config);
    let mut store = Store::new(&engine, ());
    let module = Module::new(&engine, wasm).unwrap();
    let instance = Instance::new(&mut store, &module, &[]).unwrap();

    let div = instance
        .get_typed_func::<(i32, i32), i32>(&store, "div")
        .unwrap();
    let error = div.call(&mut store, (1, 0)).unwrap_err();
    let coredump = expect_coredump(&error, TrapCode::IntegerDivisionByZero);
    assert_eq!(coredump.frame_funcs(), [(0, 0)]);
    assert_values(
        coredump.locals(0),
        &[CoreDumpValue::I32(1), CoreDumpValue::I32(0)],
    );

    let load = instance.get_typed_func::<i32, i32>(&store, "load").unwrap();
    let error = load.call(&mut store, 0x1_0000).unwrap_err();
    let coredump = expect_coredump(&error, TrapCode::MemoryOutOfBounds);
    assert_eq!(coredump.frame_funcs(), [(0, 1)]);
    assert_values(coredump.locals(0), &[CoreDumpValue::I32(0x1_0000)]);

    let recurse = instance
        .get_typed_func::<i64, ()>(&store, "recurse")
        .unwrap();
    let error = recurse.call(&mut store, 100).unwrap_err();
    let coredump = expect_coredump(&error, TrapCode::StackOverflow);
    assert_eq!(coredump.frame_funcs(), [(0, 2); 10]);
    for (index, depth) in (0..10).rev().enumerate() {
        assert_values(coredump.locals(index), &[CoreDumpValue::I64(100 + depth)]);
    }
}

#[test]
fn no_coredump_for_host_errors() {
    let engine = coredump_engine(CompilationMode::default());
    let mut store = Store::new(&engine, ());
    let mut linker = <Linker<()>>::new(&engine);
    let fail = Func::wrap(&mut store, |trap: i32| -> Result<(), Error> {
        match trap {
            0 => Err(Error::new("host error")),
            _ => Err(Error::from(TrapCode::UnreachableCodeReached)),
        }
    });
    linker.define("env", "fail", fail).unwrap();
    let wasm = r#"
        (module
            (import "env" "fail" (func $fail (param i32)))
            (func (export "run") (param i32)
                (call $fail (local.get 0))
            )
            (func (export "tail") (param i32)
                (return_call $fail (local.get 0))
            )
        )
    "#;
    let module = Module::new(&engine, wasm).unwrap();
    let instance = linker.instantiate_and_start(&mut store, &module).unwrap();
    for name in ["run", "tail"] {
        let func = instance.get_typed_func::<i32, ()>(&store, name).unwrap();
        for trap in [0, 1] {
            let error = func.call(&mut store, trap).unwrap_err();
            assert!(error.coredump().is_none(), "{name}({trap}): {error}");
        }
    }
    let fail = fail.typed::<i32, ()>(&store).unwrap();
    let error = fail.call(&mut store, 1).unwrap_err();
    assert!(error.coredump().is_none());
}

#[test]
fn coredump_includes_frames_of_all_wasm_executions() {
    let wasm = r#"
        (module $reentrant
            (import "env" "reenter" (func $reenter (param i32)))
            (func (export "wasm") (param $depth i32)
                (local $marker i64)
                (local.set $marker (i64.extend_i32_u (local.get $depth)))
                (if (i32.eqz (local.get $depth))
                    (then (unreachable))
                )
                (call $reenter (i32.sub (local.get $depth) (i32.const 1)))
            )
        )
    "#;
    for mode in COMPILATION_MODES {
        let engine = coredump_engine(mode);
        let mut store = Store::new(&engine, Vec::new());
        let reenter = Func::wrap(
            &mut store,
            |mut caller: Caller<Vec<usize>>, depth: i32| -> Result<(), Error> {
                let wasm = caller
                    .get_export("wasm")
                    .and_then(Extern::into_func)
                    .unwrap()
                    .typed::<i32, ()>(&caller)
                    .unwrap();
                let error = wasm.call(&mut caller, depth).unwrap_err();
                let len_frames = CoreDump::decode(error.coredump().unwrap()).frames.len();
                caller.data_mut().push(len_frames);
                Err(error)
            },
        );
        let module = Module::new(&engine, wasm).unwrap();
        let instance = Instance::new(&mut store, &module, &[reenter.into()]).unwrap();
        let wasm = instance.get_typed_func::<i32, ()>(&store, "wasm").unwrap();
        let error = wasm.call(&mut store, 2).unwrap_err();
        let coredump = expect_coredump(&error, TrapCode::UnreachableCodeReached);
        // The host function frames are not part of the coredump.
        assert_eq!(coredump.frame_funcs(), [(0, 1), (0, 1), (0, 1)]);
        for (index, depth) in [0, 1, 2].into_iter().enumerate() {
            assert_values(
                coredump.locals(index),
                &[
                    CoreDumpValue::I32(depth),
                    CoreDumpValue::I64(i64::from(depth)),
                ],
            );
        }
        assert_eq!(coredump.modules, ["reentrant"]);
        assert_eq!(coredump.instances.len(), 1);
        // Inner coredumps are extended by the frames of the outer Wasm executions.
        assert_eq!(store.data(), &[1, 2]);
    }
}

#[test]
fn coredump_with_multiple_instances() {
    let engine = coredump_engine(CompilationMode::default());
    let mut store = Store::new(&engine, ());
    let mut linker = <Linker<()>>::new(&engine);
    let wasm_a = r#"
        (module $a
            (memory (export "memory") 1)
            (global (export "counter") (mut i32) (i32.const 1))
            (func (export "fail") (param i32)
                (global.set 0 (local.get 0))
                (unreachable)
            )
        )
    "#;
    let wasm_b = r#"
        (module $b
            (import "a" "fail" (func $fail (param i32)))
            (import "a" "memory" (memory 1))
            (import "a" "counter" (global (mut i32)))
            (global $own i64 (i64.const 3))
            (func (export "run")
                (call $fail (i32.const 5))
            )
        )
    "#;
    let module_a = Module::new(&engine, wasm_a).unwrap();
    let module_b = Module::new(&engine, wasm_b).unwrap();
    let instance_a = linker.instantiate_and_start(&mut store, &module_a).unwrap();
    linker.instance(&mut store, "a", instance_a).unwrap();
    let instance_b1 = linker.instantiate_and_start(&mut store, &module_b).unwrap();
    let instance_b2 = linker.instantiate_and_start(&mut store, &module_b).unwrap();
    for (index, instance) in [(1, instance_b1), (2, instance_b2)] {
        let run = instance.get_typed_func::<(), ()>(&store, "run").unwrap();
        let error = run.call(&mut store, ()).unwrap_err();
        let coredump = expect_coredump(&error, TrapCode::UnreachableCodeReached);
        assert_eq!(coredump.modules, ["a", "b"]);
        let instances: Vec<_> = coredump
            .instances
            .iter()
            .map(|instance| {
                (
                    instance.module_index,
                    instance.memories.clone(),
                    instance.globals.clone(),
                )
            })
            .collect();
        assert_eq!(
            instances,
            [
                (0, vec![0], vec![0]),
                (1, vec![0], vec![0, 1]),
                (1, vec![0], vec![0, 2]),
            ]
        );
        assert_eq!(coredump.memories.len(), 1);
        assert_eq!(coredump.globals.len(), 3);
        assert_eq!(coredump.globals[0].1, GlobalValue::I32(5));
        // Function indices include imported functions.
        assert_eq!(coredump.frame_funcs(), [(0, 0), (index, 1)]);
    }
}

#[test]
fn coredump_with_tail_calls() {
    let engine = coredump_engine(CompilationMode::default());
    let mut store = Store::new(&engine, ());
    let mut linker = <Linker<()>>::new(&engine);
    let wasm_a = r#"
        (module $a
            (func (export "fail") (param i64)
                (unreachable)
            )
        )
    "#;
    let wasm_b = r#"
        (module $b
            (import "a" "fail" (func $fail (param i64)))
            (func $internal (param i64)
                (unreachable)
            )
            (func (export "run_internal") (param i32)
                (return_call $internal (i64.extend_i32_s (local.get 0)))
            )
            (func (export "run_imported") (param i32)
                (return_call $fail (i64.extend_i32_s (local.get 0)))
            )
            (func (export "call_imported") (param i32)
                (call $tail_imported (local.get 0))
            )
            (func $tail_imported (param i32)
                (return_call $fail (i64.extend_i32_s (local.get 0)))
            )
        )
    "#;
    let module_a = Module::new(&engine, wasm_a).unwrap();
    let module_b = Module::new(&engine, wasm_b).unwrap();
    let instance_a = linker.instantiate_and_start(&mut store, &module_a).unwrap();
    linker.instance(&mut store, "a", instance_a).unwrap();
    let instance_b = linker.instantiate_and_start(&mut store, &module_b).unwrap();
    let cases: [(&str, &[(u32, u32)]); 3] = [
        ("run_internal", &[(1, 1)]),
        ("run_imported", &[(0, 0)]),
        ("call_imported", &[(0, 0), (1, 4)]),
    ];
    for (name, expected) in cases {
        let func = instance_b.get_typed_func::<i32, ()>(&store, name).unwrap();
        let error = func.call(&mut store, -7).unwrap_err();
        let coredump = expect_coredump(&error, TrapCode::UnreachableCodeReached);
        assert_eq!(coredump.frame_funcs(), expected, "{name}");
        assert_values(coredump.locals(0), &[CoreDumpValue::I64(-7)]);
    }
}

#[test]
fn coredump_with_multiple_memories() {
    let wasm = r#"
        (module
            (memory 1)
            (memory i64 2 3)
            (func (export "run")
                (i32.store8 0 (i32.const 1) (i32.const 11))
                (i32.store8 1 (i64.const 70000) (i32.const 22))
                (unreachable)
            )
        )
    "#;
    let engine = coredump_engine(CompilationMode::default());
    let mut store = Store::new(&engine, ());
    let module = Module::new(&engine, wasm).unwrap();
    let instance = Instance::new(&mut store, &module, &[]).unwrap();
    let run = instance.get_typed_func::<(), ()>(&store, "run").unwrap();
    let error = run.call(&mut store, ()).unwrap_err();
    let coredump = expect_coredump(&error, TrapCode::UnreachableCodeReached);
    assert_eq!(coredump.instances[0].memories, [0, 1]);
    let memories: Vec<_> = coredump
        .memories
        .iter()
        .map(|memory| (memory.initial, memory.maximum, memory.memory64))
        .collect();
    assert_eq!(memories, [(1, None, false), (2, Some(3), true)]);
    let mem0 = coredump.memory_data(0);
    let mem1 = coredump.memory_data(1);
    assert_eq!((mem0.len(), mem0[1]), (0x1_0000, 11));
    assert_eq!((mem1.len(), mem1[70000]), (0x2_0000, 22));
}

#[test]
fn coredump_records_grown_memory() {
    let wasm = r#"
        (module
            (memory 1 4)
            (func (export "run")
                (drop (memory.grow (i32.const 2)))
                (i32.store8 (i32.const 0x2_0000) (i32.const 33))
                (unreachable)
            )
        )
    "#;
    let engine = coredump_engine(CompilationMode::default());
    let mut store = Store::new(&engine, ());
    let module = Module::new(&engine, wasm).unwrap();
    let instance = Instance::new(&mut store, &module, &[]).unwrap();
    let run = instance.get_typed_func::<(), ()>(&store, "run").unwrap();
    let error = run.call(&mut store, ()).unwrap_err();
    let coredump = expect_coredump(&error, TrapCode::UnreachableCodeReached);
    let memory = coredump.memories[0];
    assert_eq!((memory.initial, memory.maximum), (3, Some(4)));
    let data = coredump.memory_data(0);
    assert_eq!((data.len(), data[0x2_0000]), (0x3_0000, 33));
}

#[test]
fn coredump_for_trapping_start_function() {
    let wasm = r#"
        (module $start
            (func $start (local i32)
                (local.set 0 (i32.const 3))
                (unreachable)
            )
            (start $start)
        )
    "#;
    let engine = coredump_engine(CompilationMode::default());
    let mut store = Store::new(&engine, ());
    let module = Module::new(&engine, wasm).unwrap();
    let error = Instance::new(&mut store, &module, &[]).unwrap_err();
    let coredump = expect_coredump(&error, TrapCode::UnreachableCodeReached);
    assert_eq!(coredump.modules, ["start"]);
    assert_eq!(coredump.frame_funcs(), [(0, 0)]);
    assert_values(coredump.locals(0), &[CoreDumpValue::I32(3)]);
}

#[test]
fn coredump_for_resumable_calls() {
    let mut config = Config::default();
    config
        .generate_coredump(true)
        .consume_fuel(true)
        .compilation_mode(CompilationMode::Eager);
    let engine = Engine::new(&config);
    let mut store = Store::new(&engine, ());
    let wasm = r#"
        (module
            (func (export "run") (param i32) (result i32)
                (i32.div_u (i32.const 1) (local.get 0))
            )
        )
    "#;
    let module = Module::new(&engine, wasm).unwrap();
    let instance = Instance::new(&mut store, &module, &[]).unwrap();
    let run = instance.get_typed_func::<i32, i32>(&store, "run").unwrap();

    store.set_fuel(0).unwrap();
    let TypedResumableCall::OutOfFuel(invocation) = run.call_resumable(&mut store, 0).unwrap()
    else {
        panic!("expected resumable out of fuel call")
    };
    store.set_fuel(1_000).unwrap();
    let error = invocation.resume(&mut store).unwrap_err();
    let coredump = expect_coredump(&error, TrapCode::IntegerDivisionByZero);
    assert_eq!(coredump.frame_funcs(), [(0, 0)]);
    assert_values(coredump.locals(0), &[CoreDumpValue::I32(0)]);

    // Running out of fuel in non-resumable calls traps.
    store.set_fuel(0).unwrap();
    let error = run.call(&mut store, 1).unwrap_err();
    let coredump = expect_coredump(&error, TrapCode::OutOfFuel);
    assert_eq!(coredump.frame_funcs(), [(0, 0)]);
}
