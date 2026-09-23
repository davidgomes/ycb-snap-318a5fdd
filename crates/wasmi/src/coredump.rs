//! In-memory Wasm coredump image and binary encoding.
//!
//! The binary follows the [WebAssembly coredump tool convention] and is a valid
//! Wasm module. Numeric `u32` values use unsigned LEB128. Names are
//! LEB128-length-prefixed UTF-8.

use crate::{Global, Memory, ValType, handle::Handle as _};
use alloc::{boxed::Box, string::String, vec::Vec};
use core::fmt;

/// A Wasm coredump attached to a trap [`Error`](crate::Error).
pub(crate) struct Coredump {
    bytes: Box<[u8]>,
    image: Image,
}

impl fmt::Debug for Coredump {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_struct("Coredump")
            .field("bytes", &self.bytes.len())
            .field("frames", &self.image.frames.len())
            .finish()
    }
}

impl Coredump {
    /// Returns the encoded Wasm coredump bytes.
    pub(crate) fn bytes(&self) -> &[u8] {
        &self.bytes
    }

    pub(crate) fn new(executable_name: &str, frames: Vec<CapturedFrame>) -> Self {
        let mut image = Image {
            executable_name: String::from(executable_name),
            modules: Vec::new(),
            instances: Vec::new(),
            memories: Vec::new(),
            globals: Vec::new(),
            frames: Vec::new(),
        };
        image.append_frames(frames);
        let bytes = image.encode().into_boxed_slice();
        Self { bytes, image }
    }

    /// Appends older Wasm frames captured from an outer execution level.
    pub(crate) fn extend_frames(&mut self, frames: Vec<CapturedFrame>) {
        self.image.append_frames(frames);
        self.bytes = self.image.encode().into_boxed_slice();
    }
}

/// One Wasm call frame plus the instance state needed to encode it.
pub(crate) struct CapturedFrame {
    pub instance_ptr: usize,
    pub module_name: String,
    pub func_index: u32,
    pub code_offset: u32,
    pub locals: Vec<CoreVal>,
    pub memories: Vec<CapturedMemory>,
    pub globals: Vec<CapturedGlobal>,
}

pub(crate) struct CapturedMemory {
    pub id: Memory,
    pub memory64: bool,
    pub minimum: u64,
    pub maximum: Option<u64>,
    pub page_size_log2: u8,
    pub bytes: Box<[u8]>,
}

pub(crate) struct CapturedGlobal {
    pub id: Global,
    pub val_type: ValType,
    pub mutable: bool,
    pub value: CoreVal,
}

/// A value stored in a coredump frame or global initializer.
#[derive(Clone)]
pub(crate) enum CoreVal {
    I32(i32),
    I64(i64),
    F32(u32),
    F64(u64),
    /// `funcref` encoded as `ref.null`.
    FuncRef,
    /// `externref` encoded as `ref.null`.
    ExternRef,
    /// `v128` bits, low 64 then high 64.
    #[allow(dead_code)]
    V128(u64, u64),
    Missing,
}

struct Image {
    executable_name: String,
    modules: Vec<String>,
    instances: Vec<InstanceSnap>,
    memories: Vec<MemorySnap>,
    globals: Vec<GlobalSnap>,
    frames: Vec<FrameSnap>,
}

struct InstanceSnap {
    instance_ptr: usize,
    module_index: u32,
    memories: Vec<u32>,
    globals: Vec<u32>,
}

struct MemorySnap {
    id: Memory,
    memory64: bool,
    minimum: u64,
    maximum: Option<u64>,
    page_size_log2: u8,
    bytes: Box<[u8]>,
}

struct GlobalSnap {
    id: Global,
    val_type: ValType,
    mutable: bool,
    value: CoreVal,
}

struct FrameSnap {
    instance_index: u32,
    func_index: u32,
    code_offset: u32,
    locals: Vec<CoreVal>,
    stack: Vec<CoreVal>,
}

impl Image {
    fn append_frames(&mut self, frames: Vec<CapturedFrame>) {
        for frame in frames {
            let CapturedFrame {
                instance_ptr,
                module_name,
                func_index,
                code_offset,
                locals,
                memories,
                globals,
            } = frame;
            let instance_index = self.intern_instance(instance_ptr, module_name, memories, globals);
            self.frames.push(FrameSnap {
                instance_index,
                func_index,
                code_offset,
                locals,
                stack: Vec::new(),
            });
        }
    }

    fn intern_instance(
        &mut self,
        instance_ptr: usize,
        module_name: String,
        memories_in: Vec<CapturedMemory>,
        globals_in: Vec<CapturedGlobal>,
    ) -> u32 {
        if let Some(index) = self
            .instances
            .iter()
            .position(|instance| instance.instance_ptr == instance_ptr)
        {
            return u32::try_from(index).unwrap_or(u32::MAX);
        }
        let module_index = self.intern_module(module_name);
        let mut memories = Vec::with_capacity(memories_in.len());
        for memory in memories_in {
            memories.push(self.intern_memory(memory));
        }
        let mut globals = Vec::with_capacity(globals_in.len());
        for global in globals_in {
            globals.push(self.intern_global(global));
        }
        let index = self.instances.len();
        self.instances.push(InstanceSnap {
            instance_ptr,
            module_index,
            memories,
            globals,
        });
        u32::try_from(index).unwrap_or(u32::MAX)
    }

    fn intern_module(&mut self, name: String) -> u32 {
        // Each instance gets its own module entry so two instances are not
        // collapsed just because they share a display name. Callers that pass
        // the same instance are deduplicated before this runs.
        let index = self.modules.len();
        self.modules.push(name);
        u32::try_from(index).unwrap_or(u32::MAX)
    }

    fn intern_memory(&mut self, memory: CapturedMemory) -> u32 {
        if let Some(index) = self
            .memories
            .iter()
            .position(|existing| existing.id.as_raw() == memory.id.as_raw())
        {
            return u32::try_from(index).unwrap_or(u32::MAX);
        }
        let index = self.memories.len();
        self.memories.push(MemorySnap {
            id: memory.id,
            memory64: memory.memory64,
            minimum: memory.minimum,
            maximum: memory.maximum,
            page_size_log2: memory.page_size_log2,
            bytes: memory.bytes,
        });
        u32::try_from(index).unwrap_or(u32::MAX)
    }

    fn intern_global(&mut self, global: CapturedGlobal) -> u32 {
        if let Some(index) = self
            .globals
            .iter()
            .position(|existing| existing.id.as_raw() == global.id.as_raw())
        {
            return u32::try_from(index).unwrap_or(u32::MAX);
        }
        let index = self.globals.len();
        self.globals.push(GlobalSnap {
            id: global.id,
            val_type: global.val_type,
            mutable: global.mutable,
            value: global.value,
        });
        u32::try_from(index).unwrap_or(u32::MAX)
    }

    fn encode(&self) -> Vec<u8> {
        let mut module = Vec::new();
        module.extend_from_slice(&[0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00]);

        let mut core = Vec::new();
        core.push(0x00);
        write_name(&mut core, &self.executable_name);
        write_custom(&mut module, "core", &core);

        let mut modules = Vec::new();
        write_uleb(&mut modules, self.modules.len() as u64);
        for name in &self.modules {
            modules.push(0x00);
            write_name(&mut modules, name);
        }
        write_custom(&mut module, "coremodules", &modules);

        if !self.memories.is_empty() {
            let mut memories = Vec::new();
            write_uleb(&mut memories, self.memories.len() as u64);
            for memory in &self.memories {
                write_memory_type(&mut memories, memory);
            }
            write_section(&mut module, 5, &memories);
        }

        if !self.globals.is_empty() {
            let mut globals = Vec::new();
            write_uleb(&mut globals, self.globals.len() as u64);
            for global in &self.globals {
                globals.push(val_type_byte(global.val_type));
                globals.push(u8::from(global.mutable));
                write_init_expr(&mut globals, global.val_type, &global.value);
            }
            write_section(&mut module, 6, &globals);
        }

        if self.memories.iter().any(|memory| !memory.bytes.is_empty()) {
            let mut data = Vec::new();
            let count = self
                .memories
                .iter()
                .filter(|memory| !memory.bytes.is_empty())
                .count();
            write_uleb(&mut data, count as u64);
            for (index, memory) in self.memories.iter().enumerate() {
                if memory.bytes.is_empty() {
                    continue;
                }
                write_data_segment(&mut data, index as u32, memory);
            }
            write_section(&mut module, 11, &data);
        }

        let mut instances = Vec::new();
        write_uleb(&mut instances, self.instances.len() as u64);
        for instance in &self.instances {
            instances.push(0x00);
            write_uleb(&mut instances, u64::from(instance.module_index));
            write_u32_vec(&mut instances, &instance.memories);
            write_u32_vec(&mut instances, &instance.globals);
        }
        write_custom(&mut module, "coreinstances", &instances);

        let mut stack = Vec::new();
        stack.push(0x00);
        write_name(&mut stack, "main");
        write_uleb(&mut stack, self.frames.len() as u64);
        for frame in &self.frames {
            stack.push(0x00);
            write_uleb(&mut stack, u64::from(frame.instance_index));
            write_uleb(&mut stack, u64::from(frame.func_index));
            write_uleb(&mut stack, u64::from(frame.code_offset));
            write_values(&mut stack, &frame.locals);
            write_values(&mut stack, &frame.stack);
        }
        write_custom(&mut module, "corestack", &stack);
        module
    }
}

fn write_memory_type(buf: &mut Vec<u8>, memory: &MemorySnap) {
    let mut flags = 0u8;
    if memory.maximum.is_some() {
        flags |= 0x01;
    }
    if memory.memory64 {
        flags |= 0x04;
    }
    if memory.page_size_log2 != 16 {
        flags |= 0x08;
    }
    buf.push(flags);
    if memory.memory64 {
        write_uleb(buf, memory.minimum);
        if let Some(maximum) = memory.maximum {
            write_uleb(buf, maximum);
        }
    } else {
        write_uleb(buf, memory.minimum);
        if let Some(maximum) = memory.maximum {
            write_uleb(buf, maximum);
        }
    }
    if memory.page_size_log2 != 16 {
        write_uleb(buf, u64::from(memory.page_size_log2));
    }
}

fn write_data_segment(buf: &mut Vec<u8>, memory_index: u32, memory: &MemorySnap) {
    if memory_index == 0 {
        buf.push(0x00);
    } else {
        buf.push(0x02);
        write_uleb(buf, u64::from(memory_index));
    }
    if memory.memory64 {
        buf.push(0x42);
        write_sleb(buf, 0);
    } else {
        buf.push(0x41);
        write_sleb(buf, 0);
    }
    buf.push(0x0B);
    write_uleb(buf, memory.bytes.len() as u64);
    buf.extend_from_slice(&memory.bytes);
}

fn write_init_expr(buf: &mut Vec<u8>, ty: ValType, value: &CoreVal) {
    match (ty, value) {
        (ValType::I32, CoreVal::I32(value)) => {
            buf.push(0x41);
            write_sleb(buf, i64::from(*value));
        }
        (ValType::I64, CoreVal::I64(value)) => {
            buf.push(0x42);
            write_sleb(buf, *value);
        }
        (ValType::F32, CoreVal::F32(bits)) => {
            buf.push(0x43);
            buf.extend_from_slice(&bits.to_le_bytes());
        }
        (ValType::F64, CoreVal::F64(bits)) => {
            buf.push(0x44);
            buf.extend_from_slice(&bits.to_le_bytes());
        }
        (ValType::FuncRef, _) => {
            buf.push(0xD0);
            buf.push(0x70);
        }
        (ValType::ExternRef, _) => {
            buf.push(0xD0);
            buf.push(0x6F);
        }
        (ValType::V128, CoreVal::V128(lo, hi)) => {
            buf.push(0xFD);
            buf.push(0x0C);
            buf.extend_from_slice(&lo.to_le_bytes());
            buf.extend_from_slice(&hi.to_le_bytes());
        }
        _ => {
            buf.push(0x41);
            write_sleb(buf, 0);
        }
    }
    buf.push(0x0B);
}

fn write_values(buf: &mut Vec<u8>, values: &[CoreVal]) {
    write_uleb(buf, values.len() as u64);
    for value in values {
        match value {
            CoreVal::I32(value) => {
                buf.push(0x7F);
                write_sleb(buf, i64::from(*value));
            }
            CoreVal::I64(value) => {
                buf.push(0x7E);
                write_sleb(buf, *value);
            }
            CoreVal::F32(bits) => {
                buf.push(0x7D);
                buf.extend_from_slice(&bits.to_le_bytes());
            }
            CoreVal::F64(bits) => {
                buf.push(0x7C);
                buf.extend_from_slice(&bits.to_le_bytes());
            }
            CoreVal::Missing | CoreVal::FuncRef | CoreVal::ExternRef | CoreVal::V128(_, _) => {
                buf.push(0x01);
            }
        }
    }
}

fn write_u32_vec(buf: &mut Vec<u8>, values: &[u32]) {
    write_uleb(buf, values.len() as u64);
    for value in values {
        write_uleb(buf, u64::from(*value));
    }
}

fn val_type_byte(ty: ValType) -> u8 {
    match ty {
        ValType::I32 => 0x7F,
        ValType::I64 => 0x7E,
        ValType::F32 => 0x7D,
        ValType::F64 => 0x7C,
        ValType::V128 => 0x7B,
        ValType::FuncRef => 0x70,
        ValType::ExternRef => 0x6F,
    }
}

fn write_custom(module: &mut Vec<u8>, name: &str, payload: &[u8]) {
    let mut body = Vec::with_capacity(payload.len() + name.len() + 4);
    write_name(&mut body, name);
    body.extend_from_slice(payload);
    write_section(module, 0, &body);
}

fn write_section(module: &mut Vec<u8>, id: u8, payload: &[u8]) {
    module.push(id);
    write_uleb(module, payload.len() as u64);
    module.extend_from_slice(payload);
}

fn write_name(buf: &mut Vec<u8>, name: &str) {
    write_uleb(buf, name.len() as u64);
    buf.extend_from_slice(name.as_bytes());
}

fn write_uleb(buf: &mut Vec<u8>, mut value: u64) {
    loop {
        let mut byte = (value & 0x7F) as u8;
        value >>= 7;
        if value != 0 {
            byte |= 0x80;
        }
        buf.push(byte);
        if value == 0 {
            break;
        }
    }
}

fn write_sleb(buf: &mut Vec<u8>, mut value: i64) {
    loop {
        let mut byte = (value as u8) & 0x7F;
        value >>= 7;
        let done = (value == 0 && byte & 0x40 == 0) || (value == -1 && byte & 0x40 != 0);
        if !done {
            byte |= 0x80;
        }
        buf.push(byte);
        if done {
            break;
        }
    }
}

/// Reads the module name subsection out of a Wasm `name` custom section payload.
pub(crate) fn module_name_from_name_section(data: &[u8]) -> Option<String> {
    let mut pos = 0;
    while pos < data.len() {
        let id = *data.get(pos)?;
        pos += 1;
        let size = usize::try_from(read_uleb(data, &mut pos)?).ok()?;
        let content = data.get(pos..pos.checked_add(size)?)?;
        pos += size;
        if id != 0 {
            continue;
        }
        let mut name_pos = 0;
        let len = usize::try_from(read_uleb(content, &mut name_pos)?).ok()?;
        let bytes = content.get(name_pos..name_pos.checked_add(len)?)?;
        return core::str::from_utf8(bytes).ok().map(String::from);
    }
    None
}

fn read_uleb(data: &[u8], pos: &mut usize) -> Option<u64> {
    let mut result = 0u64;
    let mut shift = 0;
    loop {
        if shift > 63 {
            return None;
        }
        let byte = *data.get(*pos)?;
        *pos += 1;
        result |= u64::from(byte & 0x7F) << shift;
        if byte & 0x80 == 0 {
            return Some(result);
        }
        shift += 7;
    }
}
