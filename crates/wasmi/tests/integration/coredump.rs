//! Tests for coredump generation upon Wasm traps.

use wasmi::{
    Caller, CompilationMode, Config, Engine, Error, Extern, Func, Linker, Module, Store, TrapCode,
};
use wasmparser::{BinaryReader, DataKind, Operator, Parser, Payload, Validator};

#[derive(Debug, Clone, PartialEq)]
enum Value {
    Missing,
    I32(i32),
    I64(i64),
    F32(f32),
    F64(f64),
}

#[derive(Debug, Clone, PartialEq)]
struct Frame {
    instance: u32,
    func: u32,
    code_offset: u32,
    locals: Vec<Value>,
    stack: Vec<Value>,
}

#[derive(Debug, Clone, PartialEq)]
struct Instance {
    module: u32,
    memories: Vec<u32>,
    globals: Vec<u32>,
}

#[derive(Debug, Default)]
struct CoreDump {
    executable_name: String,
    modules: Vec<String>,
    instances: Vec<Instance>,
    thread_name: String,
    frames: Vec<Frame>,
    memories: Vec<(u64, Option<u64>)>,
    globals: Vec<(wasmparser::ValType, bool, Value)>,
    data: Vec<(u32, Vec<u8>)>,
}

fn read_values(reader: &mut BinaryReader) -> Vec<Value> {
    let len = reader.read_var_u32().unwrap();
    (0..len)
        .map(|_| match reader.read_u8().unwrap() {
            0x01 => Value::Missing,
            0x7F => Value::I32(reader.read_var_i32().unwrap()),
            0x7E => Value::I64(reader.read_var_i64().unwrap()),
            0x7D => Value::F32(f32::from_bits(reader.read_f32().unwrap().bits())),
            0x7C => Value::F64(f64::from_bits(reader.read_f64().unwrap().bits())),
            tag => panic!("unexpected value tag: {tag:#x}"),
        })
        .collect()
}

fn read_indices(reader: &mut BinaryReader) -> Vec<u32> {
    let len = reader.read_var_u32().unwrap();
    (0..len).map(|_| reader.read_var_u32().unwrap()).collect()
}

fn const_value(expr: &wasmparser::ConstExpr) -> Value {
    let mut ops = expr.get_operators_reader();
    let value = match ops.read().unwrap() {
        Operator::I32Const { value } => Value::I32(value),
        Operator::I64Const { value } => Value::I64(value),
        Operator::F32Const { value } => Value::F32(f32::from_bits(value.bits())),
        Operator::F64Const { value } => Value::F64(f64::from_bits(value.bits())),
        _ => Value::Missing,
    };
    assert!(matches!(ops.read().unwrap(), Operator::End));
    value
}

fn parse_coredump(bytes: &[u8]) -> CoreDump {
    Validator::new().validate_all(bytes).unwrap();
    let mut coredump = CoreDump::default();
    for payload in Parser::new(0).parse_all(bytes) {
        match payload.unwrap() {
            Payload::CustomSection(section) => {
                let mut reader = BinaryReader::new(section.data(), 0);
                match section.name() {
                    "core" => {
                        assert_eq!(reader.read_u8().unwrap(), 0x00);
                        coredump.executable_name = reader.read_string().unwrap().into();
                    }
                    "coremodules" => {
                        let len = reader.read_var_u32().unwrap();
                        for _ in 0..len {
                            assert_eq!(reader.read_u8().unwrap(), 0x00);
                            coredump.modules.push(reader.read_string().unwrap().into());
                        }
                    }
                    "coreinstances" => {
                        let len = reader.read_var_u32().unwrap();
                        for _ in 0..len {
                            assert_eq!(reader.read_u8().unwrap(), 0x00);
                            let module = reader.read_var_u32().unwrap();
                            let memories = read_indices(&mut reader);
                            let globals = read_indices(&mut reader);
                            coredump.instances.push(Instance {
                                module,
                                memories,
                                globals,
                            });
                        }
                    }
                    "corestack" => {
                        assert_eq!(reader.read_u8().unwrap(), 0x00);
                        coredump.thread_name = reader.read_string().unwrap().into();
                        let len = reader.read_var_u32().unwrap();
                        for _ in 0..len {
                            assert_eq!(reader.read_u8().unwrap(), 0x00);
                            let instance = reader.read_var_u32().unwrap();
                            let func = reader.read_var_u32().unwrap();
                            let code_offset = reader.read_var_u32().unwrap();
                            let locals = read_values(&mut reader);
                            let stack = read_values(&mut reader);
                            coredump.frames.push(Frame {
                                instance,
                                func,
                                code_offset,
                                locals,
                                stack,
                            });
                        }
                    }
                    name => panic!("unexpected custom section: {name}"),
                }
                assert!(reader.eof(), "trailing bytes in {}", section.name());
            }
            Payload::MemorySection(section) => {
                for memory in section {
                    let memory = memory.unwrap();
                    coredump.memories.push((memory.initial, memory.maximum));
                }
            }
            Payload::GlobalSection(section) => {
                for global in section {
                    let global = global.unwrap();
                    coredump.globals.push((
                        global.ty.content_type,
                        global.ty.mutable,
                        const_value(&global.init_expr),
                    ));
                }
            }
            Payload::DataSection(section) => {
                for data in section {
                    let data = data.unwrap();
                    let DataKind::Active {
                        memory_index,
                        offset_expr,
                    } = data.kind
                    else {
                        panic!("expected active data segment")
                    };
                    assert_eq!(const_value(&offset_expr), Value::I32(0));
                    coredump.data.push((memory_index, data.data.to_vec()));
                }
            }
            _ => {}
        }
    }
    coredump
}

fn engine_with_coredumps(mode: CompilationMode) -> Engine {
    let mut config = Config::default();
    config
        .generate_coredump(true)
        .coredump_executable_name("my-app.wasm")
        .compilation_mode(mode);
    Engine::new(&config)
}

const COMPILATION_MODES: [CompilationMode; 3] = [
    CompilationMode::Eager,
    CompilationMode::LazyTranslation,
    CompilationMode::Lazy,
];

fn call_trapping<T>(store: &mut Store<T>, instance: wasmi::Instance, name: &str) -> Error {
    instance
        .get_func(&mut *store, name)
        .unwrap()
        .call(&mut *store, &[], &mut [])
        .unwrap_err()
}

#[test]
fn coredump_disabled_by_default() {
    let engine = Engine::default();
    let mut store = Store::new(&engine, ());
    let wasm = r#"(module (func (export "run") unreachable))"#;
    let module = Module::new(&engine, wasm).unwrap();
    let instance = Linker::new(&engine)
        .instantiate_and_start(&mut store, &module)
        .unwrap();
    let error = call_trapping(&mut store, instance, "run");
    assert_eq!(error.as_trap_code(), Some(TrapCode::UnreachableCodeReached));
    assert!(error.coredump().is_none());
}

#[test]
fn coredump_default_executable_name() {
    let mut config = Config::default();
    config.generate_coredump(true);
    let engine = Engine::new(&config);
    let mut store = Store::new(&engine, ());
    let wasm = r#"(module (func (export "run") unreachable))"#;
    let module = Module::new(&engine, wasm).unwrap();
    let instance = Linker::new(&engine)
        .instantiate_and_start(&mut store, &module)
        .unwrap();
    let error = call_trapping(&mut store, instance, "run");
    let coredump = parse_coredump(error.coredump().unwrap());
    assert_eq!(coredump.executable_name, "");
    assert_eq!(coredump.frames.len(), 1);
}

#[test]
fn coredump_captures_frames_locals_memory_and_globals() {
    let wasm = r#"
        (module $my_module
            (memory 1 2)
            (global $g0 i32 (i32.const 7))
            (global $g1 (mut i64) (i64.const 0))
            (global $g2 f32 (f32.const 1.5))
            (global $g3 (mut f64) (f64.const 0))
            (func $imported_placeholder (param i32))
            (func $inner (param $a i32) (param $b f64) (result i32)
                (local $x i64) (local $y f32)
                (local.set $x (i64.const -42))
                (local.set $y (f32.const 2.5))
                (global.set $g1 (i64.const 1234567890123))
                (global.set $g3 (f64.const -0.25))
                (i32.store8 (i32.const 3) (i32.const 0xAB))
                (drop (i32.div_s (local.get $a) (i32.const 0)))
                (i32.const 0)
            )
            (func $middle (param $n i32) (result i32)
                (local $tmp i32)
                (local.set $tmp (i32.add (local.get $n) (i32.const 1)))
                (call $inner (local.get $tmp) (f64.const 3.25))
            )
            (func (export "run") (result i32)
                (call $middle (i32.const 99))
            )
        )
    "#;
    for mode in COMPILATION_MODES {
        let engine = engine_with_coredumps(mode);
        let mut store = Store::new(&engine, ());
        let module = Module::new(&engine, wasm).unwrap();
        let instance = Linker::new(&engine)
            .instantiate_and_start(&mut store, &module)
            .unwrap();
        let error = instance
            .get_typed_func::<(), i32>(&store, "run")
            .unwrap()
            .call(&mut store, ())
            .unwrap_err();
        assert_eq!(error.as_trap_code(), Some(TrapCode::IntegerDivisionByZero));
        let coredump = parse_coredump(error.coredump().unwrap());
        assert_eq!(coredump.executable_name, "my-app.wasm");
        assert_eq!(coredump.modules, ["my_module"]);
        assert_eq!(
            coredump.instances,
            [Instance {
                module: 0,
                memories: vec![0],
                globals: vec![0, 1, 2, 3],
            }]
        );
        assert_eq!(coredump.thread_name, "main");
        assert_eq!(
            coredump.frames,
            [
                Frame {
                    instance: 0,
                    func: 1,
                    code_offset: 0,
                    locals: vec![
                        Value::I32(100),
                        Value::F64(3.25),
                        Value::I64(-42),
                        Value::F32(2.5),
                    ],
                    stack: vec![],
                },
                Frame {
                    instance: 0,
                    func: 2,
                    code_offset: 0,
                    locals: vec![Value::I32(99), Value::I32(100)],
                    stack: vec![],
                },
                Frame {
                    instance: 0,
                    func: 3,
                    code_offset: 0,
                    locals: vec![],
                    stack: vec![],
                },
            ]
        );
        assert_eq!(coredump.memories, [(1, Some(2))]);
        let mut expected_memory = vec![0x00_u8; 65536];
        expected_memory[3] = 0xAB;
        assert_eq!(coredump.data, [(0, expected_memory)]);
        assert_eq!(
            coredump.globals,
            [
                (wasmparser::ValType::I32, false, Value::I32(7)),
                (wasmparser::ValType::I64, true, Value::I64(1234567890123)),
                (wasmparser::ValType::F32, false, Value::F32(1.5)),
                (wasmparser::ValType::F64, true, Value::F64(-0.25)),
            ]
        );
    }
}

#[test]
fn coredump_excludes_host_frames_and_includes_all_wasm_levels() {
    let wasm = r#"
        (module
            (import "env" "host" (func $host (param i32)))
            (func $outer (export "outer") (param $a i32)
                (local $l i64)
                (local.set $l (i64.const 5))
                (call $host (i32.add (local.get $a) (i32.const 1)))
            )
            (func $inner (export "inner") (param $b i32)
                (if (i32.eq (local.get $b) (i32.const 3))
                    (then unreachable)
                )
            )
        )
    "#;
    for mode in COMPILATION_MODES {
        let engine = engine_with_coredumps(mode);
        let mut store = Store::new(&engine, ());
        let mut linker = <Linker<()>>::new(&engine);
        let host = Func::wrap(
            &mut store,
            |mut caller: Caller<()>, value: i32| -> Result<(), Error> {
                let inner = caller
                    .get_export("inner")
                    .and_then(Extern::into_func)
                    .unwrap()
                    .typed::<i32, ()>(&caller)
                    .unwrap();
                inner.call(&mut caller, value)
            },
        );
        linker.define("env", "host", host).unwrap();
        let module = Module::new(&engine, wasm).unwrap();
        let instance = linker.instantiate_and_start(&mut store, &module).unwrap();
        let error = instance
            .get_typed_func::<i32, ()>(&store, "outer")
            .unwrap()
            .call(&mut store, 2)
            .unwrap_err();
        assert_eq!(error.as_trap_code(), Some(TrapCode::UnreachableCodeReached));
        let coredump = parse_coredump(error.coredump().unwrap());
        assert_eq!(coredump.instances.len(), 1);
        assert_eq!(
            coredump.frames,
            [
                Frame {
                    instance: 0,
                    func: 2,
                    code_offset: 0,
                    locals: vec![Value::I32(3)],
                    stack: vec![],
                },
                Frame {
                    instance: 0,
                    func: 1,
                    code_offset: 0,
                    locals: vec![Value::I32(2), Value::I64(5)],
                    stack: vec![],
                },
            ]
        );
    }
}

#[test]
fn coredump_across_instances() {
    let callee = r#"
        (module
            (memory (export "mem") 1)
            (global $g (mut i32) (i32.const 11))
            (func (export "trap") (param i32)
                (global.set $g (local.get 0))
                unreachable
            )
        )
    "#;
    let caller = r#"
        (module
            (import "callee" "trap" (func $trap (param i32)))
            (import "callee" "mem" (memory 1))
            (func (export "run")
                (call $trap (i32.const 42))
            )
        )
    "#;
    let engine = engine_with_coredumps(CompilationMode::default());
    let mut store = Store::new(&engine, ());
    let mut linker = <Linker<()>>::new(&engine);
    let callee = Module::new(&engine, callee).unwrap();
    let callee = linker.instantiate_and_start(&mut store, &callee).unwrap();
    linker.instance(&mut store, "callee", callee).unwrap();
    let caller = Module::new(&engine, caller).unwrap();
    let caller = linker.instantiate_and_start(&mut store, &caller).unwrap();
    let error = call_trapping(&mut store, caller, "run");
    let coredump = parse_coredump(error.coredump().unwrap());
    assert_eq!(coredump.modules.len(), 2);
    assert_eq!(
        coredump.instances,
        [
            Instance {
                module: 0,
                memories: vec![0],
                globals: vec![0],
            },
            Instance {
                module: 1,
                memories: vec![0],
                globals: vec![],
            },
        ]
    );
    assert_eq!(
        coredump.globals,
        [(wasmparser::ValType::I32, true, Value::I32(42))]
    );
    let frames = coredump
        .frames
        .iter()
        .map(|frame| (frame.instance, frame.func))
        .collect::<Vec<_>>();
    assert_eq!(frames, [(0, 0), (1, 1)]);
}

#[test]
fn coredump_cross_instance_tail_call() {
    let callee = r#"
        (module
            (func (export "trap") (param i32)
                unreachable
            )
        )
    "#;
    let caller = r#"
        (module
            (import "callee" "trap" (func $trap (param i32)))
            (func $tail (param i32)
                (return_call $trap (local.get 0))
            )
            (func (export "run")
                (call $tail (i32.const 42))
            )
        )
    "#;
    let engine = engine_with_coredumps(CompilationMode::default());
    let mut store = Store::new(&engine, ());
    let mut linker = <Linker<()>>::new(&engine);
    let callee = Module::new(&engine, callee).unwrap();
    let callee = linker.instantiate_and_start(&mut store, &callee).unwrap();
    linker.instance(&mut store, "callee", callee).unwrap();
    let caller = Module::new(&engine, caller).unwrap();
    let caller = linker.instantiate_and_start(&mut store, &caller).unwrap();
    let error = call_trapping(&mut store, caller, "run");
    let coredump = parse_coredump(error.coredump().unwrap());
    let frames = coredump
        .frames
        .iter()
        .map(|frame| {
            (
                coredump.instances[frame.instance as usize].module,
                frame.func,
            )
        })
        .collect::<Vec<_>>();
    assert_eq!(coredump.modules.len(), 2);
    assert_eq!(frames, [(0, 0), (1, 2)]);
    assert_eq!(coredump.frames[0].locals, [Value::I32(42)]);
}

#[test]
fn coredump_resumable_call() {
    let wasm = r#"
        (module
            (func (export "run") (param i32)
                unreachable
            )
        )
    "#;
    let engine = engine_with_coredumps(CompilationMode::default());
    let mut store = Store::new(&engine, ());
    let module = Module::new(&engine, wasm).unwrap();
    let instance = Linker::new(&engine)
        .instantiate_and_start(&mut store, &module)
        .unwrap();
    let error = instance
        .get_typed_func::<i32, ()>(&store, "run")
        .unwrap()
        .call_resumable(&mut store, 7)
        .unwrap_err();
    let coredump = parse_coredump(error.coredump().unwrap());
    assert_eq!(coredump.frames.len(), 1);
    assert_eq!(coredump.frames[0].locals, [Value::I32(7)]);
}

#[test]
fn coredump_not_generated_for_host_errors() {
    let wasm = r#"
        (module
            (import "env" "fail" (func $fail))
            (func (export "run") (call $fail))
        )
    "#;
    let engine = engine_with_coredumps(CompilationMode::default());
    let mut store = Store::new(&engine, ());
    let mut linker = <Linker<()>>::new(&engine);
    linker
        .func_wrap("env", "fail", || -> Result<(), Error> {
            Err(Error::new("host failure"))
        })
        .unwrap();
    let module = Module::new(&engine, wasm).unwrap();
    let instance = linker.instantiate_and_start(&mut store, &module).unwrap();
    let error = call_trapping(&mut store, instance, "run");
    assert!(error.coredump().is_none());
}
