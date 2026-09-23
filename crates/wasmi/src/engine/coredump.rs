//! Generation of Wasm coredumps upon Wasm traps.
//!
//! The generated coredumps follow the [Wasm coredump format].
//!
//! [Wasm coredump format]: https://github.com/WebAssembly/tool-conventions/blob/main/Coredump.md

use crate::{
    GlobalType,
    ValType,
    core::{CoreMemoryType as MemoryType, RawVal},
    engine::{Cell, CodeMap, Stack, required_cells_for_ty},
    instance::InstanceEntity,
    store::StoreInner,
};
use alloc::{boxed::Box, string::String, vec::Vec};

/// The name of the thread recorded in the `corestack` section.
const THREAD_NAME: &str = "main";

/// A Wasm coredump generated upon a Wasm trap.
///
/// # Note
///
/// The coredump is kept in a structured form so that it can be extended with
/// the stack frames of outer Wasm executions when a Wasm trap unwinds through
/// host functions that re-entered Wasm.
#[derive(Debug)]
pub struct CoreDump {
    /// The name of the executable.
    executable_name: String,
    /// The keys of all recorded Wasm modules.
    modules: Vec<usize>,
    /// All recorded Wasm instances.
    instances: Vec<InstanceImage>,
    /// All recorded linear memories.
    memories: Vec<MemoryImage>,
    /// All recorded global variables.
    globals: Vec<GlobalImage>,
    /// All recorded Wasm stack frames ordered from youngest to oldest.
    frames: Vec<FrameImage>,
    /// The encoded coredump bytes.
    bytes: Box<[u8]>,
}

/// A Wasm instance recorded in a [`CoreDump`].
#[derive(Debug)]
struct InstanceImage {
    /// Uniquely identifies the instance.
    key: usize,
    /// The index of the instance's module in the coredump's module list.
    module: u32,
    /// The indices of the instance's memories in the coredump's memory index space.
    memories: Vec<u32>,
    /// The indices of the instance's globals in the coredump's global index space.
    globals: Vec<u32>,
}

/// A linear memory recorded in a [`CoreDump`].
#[derive(Debug)]
struct MemoryImage {
    /// Uniquely identifies the linear memory.
    key: usize,
    /// The type of the linear memory at the time of recording.
    ty: MemoryType,
    /// The contents of the linear memory at the time of recording.
    data: Box<[u8]>,
}

/// A global variable recorded in a [`CoreDump`].
#[derive(Debug)]
struct GlobalImage {
    /// Uniquely identifies the global variable.
    key: usize,
    /// The type of the global variable.
    ty: GlobalType,
    /// The value of the global variable at the time of recording.
    value: RawVal,
}

/// A Wasm stack frame recorded in a [`CoreDump`].
#[derive(Debug)]
struct FrameImage {
    /// The index of the frame's instance in the coredump's instance list.
    instance: u32,
    /// The index of the frame's function within its Wasm module.
    func_index: u32,
    /// The values of the function parameters and locals.
    locals: Vec<Value>,
}

/// A value recorded in a [`CoreDump`] stack frame.
#[derive(Debug, Copy, Clone)]
enum Value {
    I32(i32),
    I64(i64),
    F32(u32),
    F64(u64),
    Missing,
}

impl CoreDump {
    /// Creates a new empty [`CoreDump`] for the executable with `executable_name`.
    pub fn new(executable_name: &str) -> Self {
        let mut coredump = Self {
            executable_name: executable_name.into(),
            modules: Vec::new(),
            instances: Vec::new(),
            memories: Vec::new(),
            globals: Vec::new(),
            frames: Vec::new(),
            bytes: Box::from([]),
        };
        coredump.encode();
        coredump
    }

    /// Returns the encoded coredump bytes.
    pub fn as_bytes(&self) -> &[u8] {
        &self.bytes
    }

    /// Appends all Wasm frames of `stack` as older frames to `self`.
    ///
    /// Also records all instances referenced by these frames together with
    /// their modules, linear memories and global variables.
    pub fn extend(&mut self, store: &StoreInner, code_map: &CodeMap, stack: &Stack) {
        let func_map = code_map.coredump_func_map();
        for frame in stack.frames() {
            let Some(instance) = frame.instance else {
                continue;
            };
            let Some((func, len_stack_slots)) = func_map.get(frame.ip) else {
                continue;
            };
            if frame.cells.len() < usize::from(len_stack_slots) {
                // The frame's stack space was never allocated so its
                // function did not begin execution.
                continue;
            }
            // Safety: frames on the stack refer to instances that are
            //         owned by `store` and thus are still alive.
            let entity = unsafe { instance.as_ref() };
            let instance = self.record_instance(store, instance.addr(), func.module_key, entity);
            let mut offset = 0;
            let locals = func
                .locals
                .iter()
                .map(|&ty| {
                    let value = read_value(frame.cells, offset, ty);
                    offset += usize::from(required_cells_for_ty(ty));
                    value
                })
                .collect();
            self.frames.push(FrameImage {
                instance,
                func_index: func.func_index,
                locals,
            });
        }
        self.encode();
    }

    /// Records the instance identified by `key` if not already recorded.
    ///
    /// Returns the index of the instance in the coredump's instance list.
    fn record_instance(
        &mut self,
        store: &StoreInner,
        key: usize,
        module_key: usize,
        entity: &InstanceEntity,
    ) -> u32 {
        if let Some(index) = self.instances.iter().position(|i| i.key == key) {
            return index as u32;
        }
        let module = match self.modules.iter().position(|&m| m == module_key) {
            Some(index) => index as u32,
            None => {
                self.modules.push(module_key);
                (self.modules.len() - 1) as u32
            }
        };
        let memories = entity
            .memories()
            .iter()
            .map(|memory| {
                let memory = store.resolve_memory(memory);
                let key = memory as *const _ as usize;
                if let Some(index) = self.memories.iter().position(|m| m.key == key) {
                    return index as u32;
                }
                self.memories.push(MemoryImage {
                    key,
                    ty: memory.dynamic_ty(),
                    data: memory.data().into(),
                });
                (self.memories.len() - 1) as u32
            })
            .collect();
        let globals = entity
            .globals()
            .iter()
            .map(|global| {
                let global = store.resolve_global(global);
                let key = global as *const _ as usize;
                if let Some(index) = self.globals.iter().position(|g| g.key == key) {
                    return index as u32;
                }
                self.globals.push(GlobalImage {
                    key,
                    ty: global.ty(),
                    value: *global.get_raw(),
                });
                (self.globals.len() - 1) as u32
            })
            .collect();
        self.instances.push(InstanceImage {
            key,
            module,
            memories,
            globals,
        });
        (self.instances.len() - 1) as u32
    }

    /// Encodes `self` into its binary representation.
    fn encode(&mut self) {
        let mut out = Vec::new();
        out.extend_from_slice(b"\0asm");
        out.extend_from_slice(&[0x01, 0x00, 0x00, 0x00]);

        let mut core = Vec::new();
        core.push(0x00);
        write_name(&mut core, &self.executable_name);
        write_custom_section(&mut out, "core", &core);

        let mut modules = Vec::new();
        write_u32(&mut modules, self.modules.len() as u32);
        for _ in &self.modules {
            modules.push(0x00);
            write_name(&mut modules, "");
        }
        write_custom_section(&mut out, "coremodules", &modules);

        let mut instances = Vec::new();
        write_u32(&mut instances, self.instances.len() as u32);
        for instance in &self.instances {
            instances.push(0x00);
            write_u32(&mut instances, instance.module);
            write_u32_list(&mut instances, &instance.memories);
            write_u32_list(&mut instances, &instance.globals);
        }
        write_custom_section(&mut out, "coreinstances", &instances);

        let mut stack = Vec::new();
        stack.push(0x00);
        write_name(&mut stack, THREAD_NAME);
        write_u32(&mut stack, self.frames.len() as u32);
        for frame in &self.frames {
            stack.push(0x00);
            write_u32(&mut stack, frame.instance);
            write_u32(&mut stack, frame.func_index);
            // Wasmi does not track Wasm code offsets.
            write_u32(&mut stack, 0);
            write_u32(&mut stack, frame.locals.len() as u32);
            for local in &frame.locals {
                write_value(&mut stack, *local);
            }
            // Wasmi does not track the types of operand stack values.
            write_u32(&mut stack, 0);
        }
        write_custom_section(&mut out, "corestack", &stack);

        let mut memories = Vec::new();
        write_u32(&mut memories, self.memories.len() as u32);
        for memory in &self.memories {
            write_memory_type(&mut memories, &memory.ty);
        }
        write_section(&mut out, 5, &memories);

        let mut globals = Vec::new();
        write_u32(&mut globals, self.globals.len() as u32);
        for global in &self.globals {
            write_global(&mut globals, global);
        }
        write_section(&mut out, 6, &globals);

        let mut data = Vec::new();
        write_u32(&mut data, self.memories.len() as u32);
        for (index, memory) in self.memories.iter().enumerate() {
            match index {
                0 => write_u32(&mut data, 0x00),
                _ => {
                    write_u32(&mut data, 0x02);
                    write_u32(&mut data, index as u32);
                }
            }
            data.push(0x41); // i32.const
            write_i64(&mut data, 0);
            data.push(0x0B); // end
            write_u32(&mut data, memory.data.len() as u32);
            data.extend_from_slice(&memory.data);
        }
        write_section(&mut out, 11, &data);

        self.bytes = out.into();
    }
}

/// Reads the value of type `ty` at `offset` from `cells`.
///
/// Returns [`Value::Missing`] if the value cannot be recovered.
fn read_value(cells: &[Cell], offset: usize, ty: ValType) -> Value {
    let Some(&cell) = cells.get(offset) else {
        return Value::Missing;
    };
    let bits = u64::from(cell);
    match ty {
        ValType::I32 => Value::I32(bits as u32 as i32),
        ValType::I64 => Value::I64(bits as i64),
        ValType::F32 => Value::F32(bits as u32),
        ValType::F64 => Value::F64(bits),
        ValType::V128 | ValType::FuncRef | ValType::ExternRef => Value::Missing,
    }
}

/// Writes an unsigned LEB128 encoded `value` to `out`.
fn write_u64(out: &mut Vec<u8>, mut value: u64) {
    loop {
        let byte = (value & 0x7F) as u8;
        value >>= 7;
        if value == 0 {
            out.push(byte);
            return;
        }
        out.push(byte | 0x80);
    }
}

/// Writes an unsigned LEB128 encoded `value` to `out`.
fn write_u32(out: &mut Vec<u8>, value: u32) {
    write_u64(out, u64::from(value));
}

/// Writes a signed LEB128 encoded `value` to `out`.
fn write_i64(out: &mut Vec<u8>, mut value: i64) {
    loop {
        let byte = (value & 0x7F) as u8;
        value >>= 7;
        let done = (value == 0 && byte & 0x40 == 0) || (value == -1 && byte & 0x40 != 0);
        if done {
            out.push(byte);
            return;
        }
        out.push(byte | 0x80);
    }
}

/// Writes a LEB128 length-prefixed UTF-8 encoded `name` to `out`.
fn write_name(out: &mut Vec<u8>, name: &str) {
    write_u32(out, name.len() as u32);
    out.extend_from_slice(name.as_bytes());
}

/// Writes a LEB128 count-prefixed list of `u32` `values` to `out`.
fn write_u32_list(out: &mut Vec<u8>, values: &[u32]) {
    write_u32(out, values.len() as u32);
    for &value in values {
        write_u32(out, value);
    }
}

/// Writes a Wasm section with `id` and `payload` to `out`.
fn write_section(out: &mut Vec<u8>, id: u8, payload: &[u8]) {
    out.push(id);
    write_u32(out, payload.len() as u32);
    out.extend_from_slice(payload);
}

/// Writes a Wasm custom section with `name` and `payload` to `out`.
fn write_custom_section(out: &mut Vec<u8>, name: &str, payload: &[u8]) {
    let mut section = Vec::with_capacity(name.len() + payload.len() + 5);
    write_name(&mut section, name);
    section.extend_from_slice(payload);
    write_section(out, 0, &section);
}

/// Writes a tagged coredump `value` to `out`.
fn write_value(out: &mut Vec<u8>, value: Value) {
    match value {
        Value::I32(value) => {
            out.push(0x7F);
            write_i64(out, i64::from(value));
        }
        Value::I64(value) => {
            out.push(0x7E);
            write_i64(out, value);
        }
        Value::F32(bits) => {
            out.push(0x7D);
            out.extend_from_slice(&bits.to_le_bytes());
        }
        Value::F64(bits) => {
            out.push(0x7C);
            out.extend_from_slice(&bits.to_le_bytes());
        }
        Value::Missing => out.push(0x01),
    }
}

/// Writes the Wasm encoding of the memory type `ty` to `out`.
fn write_memory_type(out: &mut Vec<u8>, ty: &MemoryType) {
    /// The default page size of linear memories as power of two.
    const DEFAULT_PAGE_SIZE_LOG2: u8 = 16;
    let maximum = ty.maximum();
    let custom_page_size = ty.page_size_log2() != DEFAULT_PAGE_SIZE_LOG2;
    let mut flags = 0x00;
    if maximum.is_some() {
        flags |= 0x01;
    }
    if ty.is_64() {
        flags |= 0x04;
    }
    if custom_page_size {
        flags |= 0x08;
    }
    out.push(flags);
    write_u64(out, ty.minimum());
    if let Some(maximum) = maximum {
        write_u64(out, maximum);
    }
    if custom_page_size {
        write_u32(out, u32::from(ty.page_size_log2()));
    }
}

/// Writes the Wasm encoding of the global variable's type and init expression to `out`.
fn write_global(out: &mut Vec<u8>, global: &GlobalImage) {
    let content = global.ty.content();
    let valtype = match content {
        ValType::I32 => 0x7F,
        ValType::I64 => 0x7E,
        ValType::F32 => 0x7D,
        ValType::F64 => 0x7C,
        ValType::V128 => 0x7B,
        ValType::FuncRef => 0x70,
        ValType::ExternRef => 0x6F,
    };
    out.push(valtype);
    out.push(u8::from(global.ty.mutability().is_mut()));
    let bits = global.value.to_bits64();
    match content {
        ValType::I32 => {
            out.push(0x41);
            write_i64(out, i64::from(bits as u32 as i32));
        }
        ValType::I64 => {
            out.push(0x42);
            write_i64(out, bits as i64);
        }
        ValType::F32 => {
            out.push(0x43);
            out.extend_from_slice(&(bits as u32).to_le_bytes());
        }
        ValType::F64 => {
            out.push(0x44);
            out.extend_from_slice(&bits.to_le_bytes());
        }
        ValType::V128 => {
            out.extend_from_slice(&[0xFD, 0x0C]); // v128.const
            out.extend_from_slice(&v128_bits(global.value).to_le_bytes());
        }
        ValType::FuncRef | ValType::ExternRef => {
            // Reference values cannot be meaningfully represented in a coredump.
            out.extend_from_slice(&[0xD0, valtype]); // ref.null
        }
    }
    out.push(0x0B); // end
}

/// Returns the 128-bit value of the `v128` [`RawVal`].
fn v128_bits(value: RawVal) -> u128 {
    #[cfg(feature = "simd")]
    {
        crate::V128::from(value).as_u128()
    }
    #[cfg(not(feature = "simd"))]
    {
        u128::from(value.to_bits64())
    }
}
