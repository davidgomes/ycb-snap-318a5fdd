//! Tests for Wasm coredump generation upon Wasm traps.

use wasmi::{Caller, Config, Engine, Error, Extern, Func, Linker, Module, Store, TrapCode};

/// A parsed Wasm value as stored in a coredump stack frame.
#[derive(Debug, Copy, Clone, PartialEq)]
enum Value {
    I32(i32),
    I64(i64),
    F32(f32),
    F64(f64),
    Missing,
}

#[derive(Debug, PartialEq)]
struct Instance {
    module: u32,
    memories: Vec<u32>,
    globals: Vec<u32>,
}

#[derive(Debug, PartialEq)]
struct Frame {
    instance: u32,
    func: u32,
    offset: u32,
    locals: Vec<Value>,
    stack: Vec<Value>,
}

#[derive(Debug, Default)]
struct CoreDump {
    executable_name: String,
    modules: Vec<String>,
    instances: Vec<Instance>,
    thread_name: String,
    frames: Vec<Frame>,
    /// Memories as `(flags, min, max)`.
    memories: Vec<(u8, u64, Option<u64>)>,
    /// Globals as `(valtype, mutable, init_expr_bytes)`.
    globals: Vec<(u8, bool, Vec<u8>)>,
    /// Data segments as `(memory_index, offset, bytes)`.
    data: Vec<(u32, i64, Vec<u8>)>,
}

struct Reader<'a> {
    bytes: &'a [u8],
}

impl<'a> Reader<'a> {
    fn new(bytes: &'a [u8]) -> Self {
        Self { bytes }
    }

    fn is_empty(&self) -> bool {
        self.bytes.is_empty()
    }

    fn byte(&mut self) -> u8 {
        let (first, rest) = self
            .bytes
            .split_first()
            .expect("unexpected end of coredump");
        self.bytes = rest;
        *first
    }

    fn bytes(&mut self, len: usize) -> &'a [u8] {
        let (head, rest) = self.bytes.split_at(len);
        self.bytes = rest;
        head
    }

    fn u64(&mut self) -> u64 {
        let mut result = 0;
        let mut shift = 0;
        loop {
            let byte = self.byte();
            result |= u64::from(byte & 0x7F) << shift;
            shift += 7;
            if byte & 0x80 == 0 {
                return result;
            }
        }
    }

    fn u32(&mut self) -> u32 {
        u32::try_from(self.u64()).unwrap()
    }

    fn i64(&mut self) -> i64 {
        let mut result: i64 = 0;
        let mut shift = 0;
        loop {
            let byte = self.byte();
            result |= i64::from(byte & 0x7F) << shift;
            shift += 7;
            if byte & 0x80 == 0 {
                if shift < 64 && byte & 0x40 != 0 {
                    result |= -1 << shift;
                }
                return result;
            }
        }
    }

    fn name(&mut self) -> String {
        let len = self.u32() as usize;
        String::from_utf8(self.bytes(len).to_vec()).unwrap()
    }

    fn list<T>(&mut self, mut f: impl FnMut(&mut Self) -> T) -> Vec<T> {
        let len = self.u32();
        (0..len).map(|_| f(self)).collect()
    }

    fn value(&mut self) -> Value {
        match self.byte() {
            0x7F => Value::I32(i32::try_from(self.i64()).unwrap()),
            0x7E => Value::I64(self.i64()),
            0x7D => Value::F32(f32::from_le_bytes(self.bytes(4).try_into().unwrap())),
            0x7C => Value::F64(f64::from_le_bytes(self.bytes(8).try_into().unwrap())),
            0x01 => Value::Missing,
            tag => panic!("invalid value tag: {tag:#X}"),
        }
    }
}

fn parse_coredump(bytes: &[u8]) -> CoreDump {
    wasmparser::Validator::new()
        .validate_all(bytes)
        .expect("coredump must be a valid Wasm binary");
    let mut reader = Reader::new(bytes);
    assert_eq!(reader.bytes(8), b"\0asm\x01\0\0\0");
    let mut coredump = CoreDump::default();
    while !reader.is_empty() {
        let id = reader.byte();
        let len = reader.u32() as usize;
        let mut section = Reader::new(reader.bytes(len));
        match id {
            0 => match section.name().as_str() {
                "core" => {
                    assert_eq!(section.byte(), 0x00);
                    coredump.executable_name = section.name();
                }
                "coremodules" => {
                    coredump.modules = section.list(|r| {
                        assert_eq!(r.byte(), 0x00);
                        r.name()
                    });
                }
                "coreinstances" => {
                    coredump.instances = section.list(|r| {
                        assert_eq!(r.byte(), 0x00);
                        Instance {
                            module: r.u32(),
                            memories: r.list(Reader::u32),
                            globals: r.list(Reader::u32),
                        }
                    });
                }
                "corestack" => {
                    assert_eq!(section.byte(), 0x00);
                    coredump.thread_name = section.name();
                    coredump.frames = section.list(|r| {
                        assert_eq!(r.byte(), 0x00);
                        Frame {
                            instance: r.u32(),
                            func: r.u32(),
                            offset: r.u32(),
                            locals: r.list(Reader::value),
                            stack: r.list(Reader::value),
                        }
                    });
                }
                name => panic!("unexpected custom section: {name}"),
            },
            5 => {
                coredump.memories = section.list(|r| {
                    let flags = r.byte();
                    let min = r.u64();
                    let max = (flags & 0x01 != 0).then(|| r.u64());
                    (flags, min, max)
                });
            }
            6 => {
                coredump.globals = section.list(|r| {
                    let valtype = r.byte();
                    let mutable = r.byte() == 0x01;
                    let init = match r.byte() {
                        0x41 | 0x42 => {
                            let mut init = vec![];
                            let value = r.i64();
                            init.extend_from_slice(&value.to_le_bytes());
                            init
                        }
                        0x43 => r.bytes(4).to_vec(),
                        0x44 => r.bytes(8).to_vec(),
                        op => panic!("unexpected init opcode: {op:#X}"),
                    };
                    assert_eq!(r.byte(), 0x0B);
                    (valtype, mutable, init)
                });
            }
            11 => {
                coredump.data = section.list(|r| {
                    let memory = match r.u32() {
                        0x00 => 0,
                        0x02 => r.u32(),
                        flags => panic!("unexpected data segment flags: {flags}"),
                    };
                    assert_eq!(r.byte(), 0x41);
                    let offset = r.i64();
                    assert_eq!(r.byte(), 0x0B);
                    let len = r.u32() as usize;
                    (memory, offset, r.bytes(len).to_vec())
                });
            }
            id => panic!("unexpected section id: {id}"),
        }
        assert!(section.is_empty(), "section {id} has trailing bytes");
    }
    coredump
}

fn setup(config: &Config, wat: &str) -> (Store<()>, wasmi::Instance) {
    let engine = Engine::new(config);
    let mut store = Store::new(&engine, ());
    let module = Module::new(&engine, wat).unwrap();
    let instance = Linker::new(&engine)
        .instantiate_and_start(&mut store, &module)
        .unwrap();
    (store, instance)
}

fn coredump_config() -> Config {
    let mut config = Config::default();
    config.generate_coredump(true);
    config
}

const TRAPPING_WAT: &str = r#"
    (module
        (memory 1 2)
        (global $g0 (mut i32) (i32.const 10))
        (global $g1 i64 (i64.const -5))
        (global $g2 (mut f32) (f32.const 1.5))
        (global $g3 f64 (f64.const 2.25))
        (data (i32.const 8) "\01\02\03")
        (func $trap (param i32 i64) (local f32 f64)
            (local.set 2 (f32.const 3.5))
            (local.set 3 (f64.const -0.5))
            (global.set $g0 (i32.const 42))
            (i32.store8 (i32.const 0) (i32.const 0xAB))
            (unreachable)
        )
        (func (export "run") (param i32)
            (local i64)
            (local.set 1 (i64.const 77))
            (call $trap (i32.add (local.get 0) (i32.const 1)) (i64.const -123))
        )
    )
"#;

#[test]
fn no_coredump_by_default() {
    let (mut store, instance) = setup(&Config::default(), TRAPPING_WAT);
    let run = instance.get_typed_func::<i32, ()>(&store, "run").unwrap();
    let error = run.call(&mut store, 5).unwrap_err();
    assert_eq!(error.as_trap_code(), Some(TrapCode::UnreachableCodeReached));
    assert!(error.coredump().is_none());
}

#[test]
fn coredump_on_trap() {
    let mut config = coredump_config();
    config.coredump_executable_name("my-app.wasm");
    let (mut store, instance) = setup(&config, TRAPPING_WAT);
    let run = instance.get_typed_func::<i32, ()>(&store, "run").unwrap();
    let error = run.call(&mut store, 5).unwrap_err();
    assert_eq!(error.as_trap_code(), Some(TrapCode::UnreachableCodeReached));
    let coredump = parse_coredump(error.coredump().expect("missing coredump"));
    assert_eq!(coredump.executable_name, "my-app.wasm");
    assert_eq!(coredump.modules.len(), 1);
    assert_eq!(
        coredump.instances,
        vec![Instance {
            module: 0,
            memories: vec![0],
            globals: vec![0, 1, 2, 3],
        }]
    );
    assert_eq!(
        coredump.frames,
        vec![
            Frame {
                instance: 0,
                func: 0,
                offset: 0,
                locals: vec![
                    Value::I32(6),
                    Value::I64(-123),
                    Value::F32(3.5),
                    Value::F64(-0.5),
                ],
                stack: vec![],
            },
            Frame {
                instance: 0,
                func: 1,
                offset: 0,
                locals: vec![Value::I32(5), Value::I64(77)],
                stack: vec![],
            },
        ]
    );
    assert_eq!(coredump.memories, vec![(0x01, 1, Some(2))]);
    assert_eq!(
        coredump.globals,
        vec![
            (0x7F, true, 42_i64.to_le_bytes().to_vec()),
            (0x7E, false, (-5_i64).to_le_bytes().to_vec()),
            (0x7D, true, 1.5_f32.to_le_bytes().to_vec()),
            (0x7C, false, 2.25_f64.to_le_bytes().to_vec()),
        ]
    );
    assert_eq!(coredump.data.len(), 1);
    let (memory, offset, data) = &coredump.data[0];
    assert_eq!((*memory, *offset), (0, 0));
    assert_eq!(data.len(), 65536);
    assert_eq!(&data[..11], &[0xAB, 0, 0, 0, 0, 0, 0, 0, 1, 2, 3]);
}

#[test]
fn coredump_default_executable_name() {
    let (mut store, instance) = setup(&coredump_config(), TRAPPING_WAT);
    let run = instance.get_typed_func::<i32, ()>(&store, "run").unwrap();
    let error = run.call(&mut store, 5).unwrap_err();
    let coredump = parse_coredump(error.coredump().unwrap());
    assert_eq!(coredump.executable_name, "");
}

#[test]
fn coredump_eager_compilation() {
    let mut config = coredump_config();
    config.compilation_mode(wasmi::CompilationMode::Eager);
    let (mut store, instance) = setup(&config, TRAPPING_WAT);
    let run = instance.get_typed_func::<i32, ()>(&store, "run").unwrap();
    let error = run.call(&mut store, 1).unwrap_err();
    let coredump = parse_coredump(error.coredump().unwrap());
    let funcs: Vec<_> = coredump.frames.iter().map(|f| f.func).collect();
    assert_eq!(funcs, vec![0, 1]);
    assert_eq!(coredump.frames[0].locals[0], Value::I32(2));
}

#[test]
fn coredump_resumable_call() {
    let (mut store, instance) = setup(&coredump_config(), TRAPPING_WAT);
    let run = instance.get_typed_func::<i32, ()>(&store, "run").unwrap();
    let error = run.call_resumable(&mut store, 5).unwrap_err();
    let coredump = parse_coredump(error.coredump().unwrap());
    assert_eq!(coredump.frames.len(), 2);
}

#[test]
fn no_coredump_for_host_errors() {
    let config = coredump_config();
    let engine = Engine::new(&config);
    let mut store = Store::new(&engine, ());
    let mut linker = <Linker<()>>::new(&engine);
    linker
        .func_wrap("env", "fail", || -> Result<(), Error> {
            Err(Error::new("host failure"))
        })
        .unwrap();
    let wat = r#"
        (module
            (import "env" "fail" (func $fail))
            (func (export "run") (call $fail))
        )
    "#;
    let module = Module::new(&engine, wat).unwrap();
    let instance = linker.instantiate_and_start(&mut store, &module).unwrap();
    let run = instance.get_typed_func::<(), ()>(&store, "run").unwrap();
    let error = run.call(&mut store, ()).unwrap_err();
    assert!(error.coredump().is_none());
}

#[test]
fn coredump_stack_overflow() {
    let wat = r#"
        (module
            (func $f (export "run") (param i32)
                (call $f (i32.add (local.get 0) (i32.const 1)))
            )
        )
    "#;
    let (mut store, instance) = setup(&coredump_config(), wat);
    let run = instance.get_typed_func::<i32, ()>(&store, "run").unwrap();
    let error = run.call(&mut store, 0).unwrap_err();
    assert_eq!(error.as_trap_code(), Some(TrapCode::StackOverflow));
    let coredump = parse_coredump(error.coredump().unwrap());
    assert!(!coredump.frames.is_empty());
    let depth = coredump.frames.len() as i32;
    for (n, frame) in coredump.frames.iter().enumerate() {
        assert_eq!(frame.func, 0);
        assert_eq!(frame.locals, vec![Value::I32(depth - 1 - n as i32)]);
    }
}

#[test]
fn coredump_reentrant_host_calls() {
    let config = coredump_config();
    let engine = Engine::new(&config);
    let mut store = Store::new(&engine, ());
    let mut linker = <Linker<()>>::new(&engine);
    linker
        .func_wrap(
            "env",
            "reenter",
            |mut caller: Caller<()>, input: i32| -> Result<(), Error> {
                let inner = caller
                    .get_export("inner")
                    .and_then(Extern::into_func)
                    .unwrap()
                    .typed::<i32, ()>(&caller)
                    .unwrap();
                inner.call(&mut caller, input * 2)
            },
        )
        .unwrap();
    let wat = r#"
        (module
            (import "env" "reenter" (func $reenter (param i32)))
            (func $inner (export "inner") (param i32)
                (if (i32.eq (local.get 0) (i32.const 0))
                    (then (unreachable))
                )
                (call $reenter (i32.sub (local.get 0) (i32.const 1)))
            )
            (func (export "outer") (param i32)
                (call $inner (local.get 0))
            )
        )
    "#;
    let module = Module::new(&engine, wat).unwrap();
    let instance = linker.instantiate_and_start(&mut store, &module).unwrap();
    let outer = instance.get_typed_func::<i32, ()>(&store, "outer").unwrap();
    // outer(1) -> inner(1) -> reenter(0) -> inner(0) -> trap
    let error = outer.call(&mut store, 1).unwrap_err();
    assert_eq!(error.as_trap_code(), Some(TrapCode::UnreachableCodeReached));
    let coredump = parse_coredump(error.coredump().unwrap());
    let frames: Vec<_> = coredump
        .frames
        .iter()
        .map(|f| (f.func, f.locals.clone()))
        .collect();
    assert_eq!(
        frames,
        vec![
            (1, vec![Value::I32(0)]),
            (1, vec![Value::I32(1)]),
            (2, vec![Value::I32(1)]),
        ]
    );
    assert_eq!(coredump.instances.len(), 1);
    assert!(coredump.frames.iter().all(|f| f.instance == 0));
}

#[test]
fn coredump_multiple_instances() {
    let config = coredump_config();
    let engine = Engine::new(&config);
    let mut store = Store::new(&engine, ());
    let mut linker = <Linker<()>>::new(&engine);
    let callee_wat = r#"
        (module
            (memory (export "mem") 1)
            (global (export "g") (mut i64) (i64.const 7))
            (func (export "trap") (param f64)
                (unreachable)
            )
        )
    "#;
    let caller_wat = r#"
        (module
            (import "callee" "mem" (memory 1))
            (import "callee" "g" (global (mut i64)))
            (import "callee" "trap" (func $trap (param f64)))
            (global i32 (i32.const 3))
            (func (export "run")
                (call $trap (f64.const 4.5))
            )
        )
    "#;
    let callee = Module::new(&engine, callee_wat).unwrap();
    let callee = linker.instantiate_and_start(&mut store, &callee).unwrap();
    linker.instance(&mut store, "callee", callee).unwrap();
    let caller = Module::new(&engine, caller_wat).unwrap();
    let caller = linker.instantiate_and_start(&mut store, &caller).unwrap();
    let run = caller.get_typed_func::<(), ()>(&store, "run").unwrap();
    let error = run.call(&mut store, ()).unwrap_err();
    let coredump = parse_coredump(error.coredump().unwrap());
    assert_eq!(coredump.modules.len(), 2);
    assert_eq!(
        coredump.instances,
        vec![
            Instance {
                module: 0,
                memories: vec![0],
                globals: vec![0],
            },
            Instance {
                module: 1,
                memories: vec![0],
                globals: vec![0, 1],
            },
        ]
    );
    assert_eq!(
        coredump.frames,
        vec![
            Frame {
                instance: 0,
                func: 0,
                offset: 0,
                locals: vec![Value::F64(4.5)],
                stack: vec![],
            },
            Frame {
                instance: 1,
                func: 1,
                offset: 0,
                locals: vec![],
                stack: vec![],
            },
        ]
    );
    assert_eq!(coredump.memories.len(), 1);
    assert_eq!(coredump.globals.len(), 2);
}

#[test]
fn coredump_host_func_is_excluded() {
    let wat = r#"
        (module
            (func (export "run") (param i32) (result i32)
                (i32.div_s (i32.const 1) (local.get 0))
            )
        )
    "#;
    let (mut store, instance) = setup(&coredump_config(), wat);
    let run = instance.get_func(&store, "run").unwrap();
    let host = Func::wrap(
        &mut store,
        move |mut caller: Caller<()>, x: i32| -> Result<i32, Error> {
            run.typed::<i32, i32>(&caller)?.call(&mut caller, x)
        },
    );
    let error = host
        .typed::<i32, i32>(&store)
        .unwrap()
        .call(&mut store, 0)
        .unwrap_err();
    assert_eq!(error.as_trap_code(), Some(TrapCode::IntegerDivisionByZero));
    let coredump = parse_coredump(error.coredump().unwrap());
    assert_eq!(coredump.frames.len(), 1);
    assert_eq!(coredump.frames[0].locals, vec![Value::I32(0)]);
}
