//! Opt-in Wasm coredump generation.

use wasmi::{Caller, Config, Engine, Error, Func, Linker, Module, Store, TrapCode};

fn engine() -> Engine {
    let mut config = Config::default();
    config.generate_coredump(true);
    config.coredump_executable_name("demo.wasm");
    Engine::new(&config)
}

fn instantiate(store: &mut Store<()>, wasm: &str) -> wasmi::Instance {
    let module = Module::new(store.engine(), wasm).unwrap();
    Linker::new(store.engine())
        .instantiate_and_start(store, &module)
        .unwrap()
}

#[test]
fn disabled_by_default() {
    let engine = Engine::default();
    let mut store = Store::new(&engine, ());
    let instance = instantiate(&mut store, "(module (func (export \"t\") unreachable))");
    let func = instance.get_typed_func::<(), ()>(&store, "t").unwrap();
    let error = func.call(&mut store, ()).unwrap_err();
    assert_eq!(error.as_trap_code(), Some(TrapCode::UnreachableCodeReached));
    assert!(error.coredump().is_none());
}

#[test]
fn trap_carries_memory_global_and_stack() {
    let engine = engine();
    let mut store = Store::new(&engine, ());
    let wasm = r#"
        (module $app
            (memory (export "mem") 1 2)
            (global (export "g") (mut i32) (i32.const 7))
            (func $inner (param i32) (local i64)
                local.get 0
                unreachable
            )
            (func (export "run") (param i32)
                local.get 0
                call $inner
            )
        )
    "#;
    let instance = instantiate(&mut store, wasm);
    instance
        .get_memory(&store, "mem")
        .unwrap()
        .write(&mut store, 0, &[0x11, 0x22, 0x33, 0x44])
        .unwrap();
    let func = instance.get_typed_func::<i32, ()>(&store, "run").unwrap();
    let error = func.call(&mut store, 42).unwrap_err();
    assert_eq!(error.as_trap_code(), Some(TrapCode::UnreachableCodeReached));
    let bytes = error.coredump().expect("coredump");
    Module::new(store.engine(), bytes).expect("coredump is a valid Wasm module");
    let dump = parse(bytes);

    assert_eq!(dump.executable, "demo.wasm");
    assert_eq!(dump.modules, vec!["app".to_string()]);
    assert_eq!(dump.instances.len(), 1);
    assert_eq!(dump.instances[0].module, 0);
    assert_eq!(dump.instances[0].memories, vec![0]);
    assert_eq!(dump.instances[0].globals, vec![0]);

    assert_eq!(dump.memories.len(), 1);
    assert_eq!(dump.memories[0].minimum, 1);
    assert_eq!(dump.memories[0].maximum, Some(2));
    assert!(dump.memories[0].bytes.starts_with(&[0x11, 0x22, 0x33, 0x44]));

    assert_eq!(dump.globals.len(), 1);
    assert_eq!(dump.globals[0].ty, 0x7F);
    assert_eq!(dump.globals[0].mutable, 1);
    assert_eq!(dump.globals[0].value, Value::I32(7));

    assert_eq!(dump.thread, "main");
    assert_eq!(dump.frames.len(), 2, "frames: {:?}", dump.frames);
    let inner = &dump.frames[0];
    assert_eq!(inner.instance, 0);
    assert_eq!(inner.func, 0);
    assert_ne!(inner.offset, 0, "unreachable offset");
    assert_eq!(inner.locals, vec![Value::I32(42), Value::I64(0)]);
    assert_eq!(inner.stack, vec![Value::I32(42)]);

    let outer = &dump.frames[1];
    assert_eq!(outer.instance, 0);
    assert_eq!(outer.func, 1);
    assert_eq!(outer.locals, vec![Value::I32(42)]);
    assert_eq!(outer.stack, vec![Value::I32(42)]);
}

#[test]
fn reentrant_host_call_keeps_every_wasm_frame() {
    let engine = engine();
    let mut store = Store::new(&engine, ());
    let wasm = r#"
        (module $app
            (import "env" "host" (func $host (param i32)))
            (func (export "run") (param i32)
                (local i32)
                local.get 0
                call $host
            )
            (func (export "trap") (param i64) (result i64)
                local.get 0
                unreachable
            )
        )
    "#;
    let module = Module::new(store.engine(), wasm).unwrap();
    let host = Func::wrap(&mut store, |mut caller: Caller<()>, _param: i32| {
        let trap = caller
            .get_export("trap")
            .and_then(wasmi::Extern::into_func)
            .unwrap()
            .typed::<i64, i64>(&caller)
            .unwrap();
        trap.call(&mut caller, 99)?;
        Ok::<(), Error>(())
    });
    let mut linker = Linker::new(store.engine());
    linker.define("env", "host", host).unwrap();
    let instance = linker.instantiate_and_start(&mut store, &module).unwrap();
    let func = instance.get_typed_func::<i32, ()>(&store, "run").unwrap();
    let error = func.call(&mut store, 5).unwrap_err();
    assert_eq!(error.as_trap_code(), Some(TrapCode::UnreachableCodeReached));
    let dump = parse(error.coredump().expect("coredump"));
    assert_eq!(dump.frames.len(), 2, "frames: {:?}", dump.frames);
    assert_eq!(dump.frames[0].func, 2);
    assert_eq!(dump.frames[0].locals, vec![Value::I64(99)]);
    assert_eq!(dump.frames[0].stack, vec![Value::I64(99)]);
    assert_eq!(dump.frames[1].func, 1);
    assert_eq!(dump.frames[1].locals, vec![Value::I32(5), Value::I32(0)]);
    assert_eq!(dump.frames[1].stack, vec![Value::I32(5)]);
}

#[test]
fn floats_and_out_of_fuel() {
    let mut config = Config::default();
    config.generate_coredump(true);
    config.consume_fuel(true);
    config.compilation_mode(wasmi::CompilationMode::Eager);
    let engine = Engine::new(&config);
    let mut store = Store::new(&engine, ());
    store.set_fuel(100_000).unwrap();
    let wasm = r#"
        (module
            (func (export "floats") (param f32 f64) (local f32)
                local.get 0
                local.get 1
                unreachable
            )
            (func (export "burn")
                (loop $l
                    br $l
                )
            )
        )
    "#;
    let instance = instantiate(&mut store, wasm);
    let floats = instance
        .get_typed_func::<(f32, f64), ()>(&store, "floats")
        .unwrap();
    let error = floats.call(&mut store, (1.5, -2.25)).unwrap_err();
    let dump = parse(error.coredump().expect("coredump"));
    assert_eq!(
        dump.frames[0].locals,
        vec![
            Value::F32(1.5f32.to_bits()),
            Value::F64((-2.25f64).to_bits()),
            Value::F32(0f32.to_bits()),
        ]
    );
    assert_eq!(
        dump.frames[0].stack,
        vec![
            Value::F32(1.5f32.to_bits()),
            Value::F64((-2.25f64).to_bits()),
        ]
    );

    // The function is already compiled. A few units of fuel run out inside the loop.
    store.set_fuel(8).unwrap();
    let error = instance
        .get_typed_func::<(), ()>(&store, "burn")
        .unwrap()
        .call(&mut store, ())
        .unwrap_err();
    assert_eq!(error.as_trap_code(), Some(TrapCode::OutOfFuel));
    let dump = parse(error.coredump().expect("fuel coredump"));
    assert_eq!(dump.frames.len(), 1);
    assert_eq!(dump.frames[0].func, 1);
}

#[test]
fn host_error_has_no_coredump() {
    let engine = engine();
    let mut store = Store::new(&engine, ());
    let host = Func::wrap(&mut store, || -> Result<(), Error> { Err(Error::new("nope")) });
    let mut linker = Linker::new(store.engine());
    linker.define("env", "host", host).unwrap();
    let wasm = r#"
        (module
            (import "env" "host" (func $host))
            (func (export "run") call $host)
        )
    "#;
    let module = Module::new(store.engine(), wasm).unwrap();
    let instance = linker.instantiate_and_start(&mut store, &module).unwrap();
    let error = instance
        .get_typed_func::<(), ()>(&store, "run")
        .unwrap()
        .call(&mut store, ())
        .unwrap_err();
    assert!(error.coredump().is_none());
}

#[derive(Debug)]
struct Dump {
    executable: String,
    modules: Vec<String>,
    instances: Vec<Instance>,
    memories: Vec<Memory>,
    globals: Vec<Global>,
    thread: String,
    frames: Vec<Frame>,
}

#[derive(Debug)]
struct Instance {
    module: u32,
    memories: Vec<u32>,
    globals: Vec<u32>,
}

#[derive(Debug)]
struct Memory {
    minimum: u64,
    maximum: Option<u64>,
    bytes: Vec<u8>,
}

#[derive(Debug)]
struct Global {
    ty: u8,
    mutable: u8,
    value: Value,
}

#[derive(Debug)]
struct Frame {
    instance: u32,
    func: u32,
    offset: u32,
    locals: Vec<Value>,
    stack: Vec<Value>,
}

#[derive(Debug, PartialEq)]
enum Value {
    I32(i32),
    I64(i64),
    F32(u32),
    F64(u64),
    Missing,
}

fn parse(bytes: &[u8]) -> Dump {
    assert_eq!(&bytes[..4], b"\0asm");
    assert_eq!(&bytes[4..8], &1u32.to_le_bytes());
    let mut index = 8;
    let mut dump = Dump {
        executable: String::new(),
        modules: Vec::new(),
        instances: Vec::new(),
        memories: Vec::new(),
        globals: Vec::new(),
        thread: String::new(),
        frames: Vec::new(),
    };
    let mut data_segments = Vec::new();
    while index < bytes.len() {
        let id = bytes[index];
        index += 1;
        let (size, next) = read_uleb(bytes, index);
        index = next;
        let end = index + size as usize;
        let payload = &bytes[index..end];
        index = end;
        match id {
            0 => parse_custom(&mut dump, payload),
            5 => dump.memories = parse_memories(payload),
            6 => dump.globals = parse_globals(payload),
            11 => data_segments = parse_data(payload),
            other => panic!("unexpected section {other}"),
        }
    }
    for (memory_index, bytes) in data_segments {
        dump.memories[memory_index as usize].bytes = bytes;
    }
    dump
}

fn parse_custom(dump: &mut Dump, payload: &[u8]) {
    let (name, content_at) = read_name(payload, 0);
    let content = &payload[content_at..];
    match name.as_str() {
        "core" => {
            assert_eq!(content[0], 0x00);
            dump.executable = read_name(content, 1).0;
        }
        "coremodules" => {
            let (count, mut index) = read_uleb(content, 0);
            for _ in 0..count {
                assert_eq!(content[index], 0x00);
                let (name, next) = read_name(content, index + 1);
                dump.modules.push(name);
                index = next;
            }
        }
        "coreinstances" => {
            let (count, mut index) = read_uleb(content, 0);
            for _ in 0..count {
                assert_eq!(content[index], 0x00);
                index += 1;
                let (module, next) = read_uleb(content, index);
                let (memory_count, next) = read_uleb(content, next);
                let mut memories = Vec::new();
                index = next;
                for _ in 0..memory_count {
                    let (memory, next) = read_uleb(content, index);
                    memories.push(memory as u32);
                    index = next;
                }
                let (global_count, next) = read_uleb(content, index);
                let mut globals = Vec::new();
                index = next;
                for _ in 0..global_count {
                    let (global, next) = read_uleb(content, index);
                    globals.push(global as u32);
                    index = next;
                }
                dump.instances.push(Instance {
                    module: module as u32,
                    memories,
                    globals,
                });
            }
        }
        "corestack" => {
            assert_eq!(content[0], 0x00);
            let (thread, mut index) = read_name(content, 1);
            dump.thread = thread;
            let (count, next) = read_uleb(content, index);
            index = next;
            for _ in 0..count {
                assert_eq!(content[index], 0x00);
                index += 1;
                let (instance, next) = read_uleb(content, index);
                let (func, next) = read_uleb(content, next);
                let (offset, next) = read_uleb(content, next);
                let (locals, next) = read_values(content, next);
                let (stack, next) = read_values(content, next);
                index = next;
                dump.frames.push(Frame {
                    instance: instance as u32,
                    func: func as u32,
                    offset: offset as u32,
                    locals,
                    stack,
                });
            }
        }
        other => panic!("unexpected custom section {other}"),
    }
}

fn parse_memories(payload: &[u8]) -> Vec<Memory> {
    let (count, mut index) = read_uleb(payload, 0);
    let mut memories = Vec::new();
    for _ in 0..count {
        let flags = payload[index];
        index += 1;
        let (minimum, next) = read_uleb(payload, index);
        index = next;
        let maximum = if flags & 0x01 != 0 {
            let (maximum, next) = read_uleb(payload, index);
            index = next;
            Some(maximum)
        } else {
            None
        };
        if flags & 0x08 != 0 {
            index += 1;
        }
        memories.push(Memory {
            minimum,
            maximum,
            bytes: Vec::new(),
        });
    }
    memories
}

fn parse_globals(payload: &[u8]) -> Vec<Global> {
    let (count, mut index) = read_uleb(payload, 0);
    let mut globals = Vec::new();
    for _ in 0..count {
        let ty = payload[index];
        let mutable = payload[index + 1];
        index += 2;
        let opcode = payload[index];
        index += 1;
        let (value, next) = match opcode {
            0x41 => {
                let (value, next) = read_sleb(payload, index);
                (Value::I32(value as i32), next)
            }
            0x42 => {
                let (value, next) = read_sleb(payload, index);
                (Value::I64(value), next)
            }
            0x43 => {
                let bits = u32::from_le_bytes(payload[index..index + 4].try_into().unwrap());
                (Value::F32(bits), index + 4)
            }
            0x44 => {
                let bits = u64::from_le_bytes(payload[index..index + 8].try_into().unwrap());
                (Value::F64(bits), index + 8)
            }
            other => panic!("unexpected global opcode {other:#x}"),
        };
        index = next;
        assert_eq!(payload[index], 0x0B);
        index += 1;
        globals.push(Global { ty, mutable, value });
    }
    globals
}

fn parse_data(payload: &[u8]) -> Vec<(u32, Vec<u8>)> {
    let (count, mut index) = read_uleb(payload, 0);
    let mut segments = Vec::new();
    for _ in 0..count {
        let flags = payload[index];
        index += 1;
        let memory = if flags == 0x02 {
            let (memory, next) = read_uleb(payload, index);
            index = next;
            memory as u32
        } else {
            assert_eq!(flags, 0x00);
            0
        };
        let opcode = payload[index];
        index += 1;
        let (_offset, next) = read_sleb(payload, index);
        index = next;
        assert_eq!(payload[index], 0x0B, "opcode {opcode:#x}");
        index += 1;
        let (len, next) = read_uleb(payload, index);
        index = next;
        let bytes = payload[index..index + len as usize].to_vec();
        index += len as usize;
        segments.push((memory, bytes));
    }
    segments
}

fn read_values(data: &[u8], index: usize) -> (Vec<Value>, usize) {
    let (count, mut index) = read_uleb(data, index);
    let mut values = Vec::new();
    for _ in 0..count {
        let (value, next) = read_value(data, index);
        values.push(value);
        index = next;
    }
    (values, index)
}

fn read_value(data: &[u8], index: usize) -> (Value, usize) {
    match data[index] {
        0x01 => (Value::Missing, index + 1),
        0x7F => {
            let (value, next) = read_sleb(data, index + 1);
            (Value::I32(value as i32), next)
        }
        0x7E => {
            let (value, next) = read_sleb(data, index + 1);
            (Value::I64(value), next)
        }
        0x7D => {
            let bits = u32::from_le_bytes(data[index + 1..index + 5].try_into().unwrap());
            (Value::F32(bits), index + 5)
        }
        0x7C => {
            let bits = u64::from_le_bytes(data[index + 1..index + 9].try_into().unwrap());
            (Value::F64(bits), index + 9)
        }
        other => panic!("bad value tag {other:#x}"),
    }
}

fn read_name(data: &[u8], index: usize) -> (String, usize) {
    let (len, index) = read_uleb(data, index);
    let end = index + len as usize;
    let name = String::from_utf8(data[index..end].to_vec()).unwrap();
    (name, end)
}

fn read_uleb(data: &[u8], mut index: usize) -> (u64, usize) {
    let mut result = 0u64;
    let mut shift = 0;
    loop {
        let byte = data[index];
        index += 1;
        result |= u64::from(byte & 0x7F) << shift;
        if byte & 0x80 == 0 {
            return (result, index);
        }
        shift += 7;
    }
}

fn read_sleb(data: &[u8], mut index: usize) -> (i64, usize) {
    let mut result = 0i64;
    let mut shift = 0;
    loop {
        let byte = data[index];
        index += 1;
        result |= i64::from(byte & 0x7F) << shift;
        shift += 7;
        if byte & 0x80 == 0 {
            if shift < 64 && byte & 0x40 != 0 {
                result |= !0 << shift;
            }
            return (result, index);
        }
    }
}
