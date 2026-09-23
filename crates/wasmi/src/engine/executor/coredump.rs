//! Opt-in Wasm coredump generation.
//!
//! The image follows the WebAssembly tool-conventions coredump format: a valid
//! Wasm binary whose memories, globals, and data segments hold process state and
//! whose `core`, `coremodules`, `coreinstances`, and `corestack` custom sections
//! describe the faulting executable, modules, instances, and stack.

use super::{
    CodeMap,
    ExecutionOutcome,
    handler::{CoredumpFrame, Stack},
};
use crate::{
    Error,
    Global,
    Memory,
    Mutability,
    Store,
    TrapCode,
    Val,
    ValType,
    engine::func_debug::{DebugOperand, FuncDebugInfo},
    handle::Handle,
    instance::InstanceEntity,
};
use alloc::{string::String, vec, vec::Vec};
use core::ptr;

/// How frames on `stack` relate to an already captured coredump.
#[derive(Copy, Clone)]
enum FrameMode {
    /// `stack` holds the trap. Its youngest frame is the trap site.
    Trap,
    /// `stack` is an outer invocation. Every frame is older than the frames
    /// already stored in the coredump.
    Extend,
}

/// Attaches or extends a coredump on a Wasm execution `outcome`.
///
/// Out-of-fuel stays resumable here. [`finalize_non_resumable`] turns that
/// suspension into a trap once the caller has decided not to resume.
pub(super) fn enrich<T>(
    store: &Store<T>,
    stack: &Stack,
    code: &CodeMap,
    outcome: ExecutionOutcome,
) -> ExecutionOutcome {
    if !store.engine().config().get_generate_coredump() {
        return outcome;
    }
    match outcome {
        ExecutionOutcome::Error(error) => {
            ExecutionOutcome::Error(enrich_error(store, stack, code, error, FrameMode::Trap))
        }
        ExecutionOutcome::OutOfFuel(error) => ExecutionOutcome::OutOfFuel(error),
        ExecutionOutcome::Host(error) => {
            let error = error.map_error(|error| {
                enrich_error(store, stack, code, error, FrameMode::Extend)
            });
            ExecutionOutcome::Host(error)
        }
    }
}

/// Converts `outcome` into the [`Error`] returned by a non-resumable call.
///
/// When coredump generation is enabled, an out-of-fuel suspension becomes an
/// out-of-fuel trap that carries a coredump of `stack`.
pub(super) fn finalize_non_resumable<T>(
    store: &Store<T>,
    stack: &Stack,
    code: &CodeMap,
    outcome: ExecutionOutcome,
) -> Error {
    match outcome {
        ExecutionOutcome::OutOfFuel(_) if store.engine().config().get_generate_coredump() => {
            let bytes = build(store, stack, code, &[], FrameMode::Trap);
            Error::from_trap_with_coredump(TrapCode::OutOfFuel, bytes)
        }
        other => other.into_non_resumable(),
    }
}

/// Attaches a fresh coredump to a Wasm trap, or appends `stack` frames to one
/// that an inner invocation already captured.
fn enrich_error<T>(
    store: &Store<T>,
    stack: &Stack,
    code: &CodeMap,
    error: Error,
    mode: FrameMode,
) -> Error {
    if error.coredump().is_some() {
        return extend_coredump(store, stack, code, error);
    }
    let FrameMode::Trap = mode else {
        return error;
    };
    let Some(trap) = trap_code_of(&error) else {
        return error;
    };
    let bytes = build(store, stack, code, &[], FrameMode::Trap);
    Error::from_trap_with_coredump(trap, bytes)
}

/// Returns the trap code when `error` is a bare Wasm trap.
///
/// Host errors and other failures are left unchanged so coredumps stay limited
/// to traps raised by Wasm execution.
fn trap_code_of(error: &Error) -> Option<TrapCode> {
    match error.kind() {
        crate::error::ErrorKind::TrapCode(code) => Some(*code),
        _ => None,
    }
}

/// Rebuilds a coredump, keeping inner frames and appending frames from `stack`.
fn extend_coredump<T>(
    store: &Store<T>,
    stack: &Stack,
    code: &CodeMap,
    error: Error,
) -> Error {
    let Some(existing) = error.coredump().and_then(extract_frames) else {
        return error;
    };
    let bytes = build(store, stack, code, &existing, FrameMode::Extend);
    error.with_coredump_bytes(bytes)
}

/// Builds a coredump image.
///
/// `existing` frames are encoded first (younger), then frames taken from `stack`.
fn build<T>(
    store: &Store<T>,
    stack: &Stack,
    code: &CodeMap,
    existing: &[Vec<u8>],
    mode: FrameMode,
) -> Vec<u8> {
    let image = capture(store, stack, code, mode);
    let mut frames = Vec::with_capacity(existing.len() + image.frames.len());
    frames.extend(existing.iter().cloned());
    frames.extend(image.frames);
    encode(
        store.engine().config().get_coredump_executable_name(),
        &image.modules,
        &image.instances,
        &image.memories,
        &image.globals,
        &frames,
    )
}

struct MemoryImage {
    is_64: bool,
    minimum: u64,
    maximum: Option<u64>,
    page_size_log2: u8,
    bytes: Vec<u8>,
}

struct GlobalImage {
    ty: ValType,
    mutable: bool,
    value: Val,
}

struct InstanceImage {
    module_index: u32,
    memories: Vec<u32>,
    globals: Vec<u32>,
}

struct Captured {
    modules: Vec<String>,
    instances: Vec<InstanceImage>,
    memories: Vec<MemoryImage>,
    globals: Vec<GlobalImage>,
    frames: Vec<Vec<u8>>,
}

fn capture<T>(store: &Store<T>, stack: &Stack, code: &CodeMap, mode: FrameMode) -> Captured {
    let mut modules: Vec<(usize, String)> = Vec::new();
    let mut instances = Vec::new();
    let mut instance_ptrs = Vec::new();
    let mut memories = Vec::new();
    let mut memory_keys = Vec::new();
    let mut globals = Vec::new();
    let mut global_keys = Vec::new();

    for entity in store.inner.instance_entities().filter(|entity| entity.is_initialized()) {
        instance_ptrs.push(ptr::from_ref(entity));
        let module_index = module_index(&mut modules, entity);
        let mut memory_indices = Vec::with_capacity(entity.memories().len());
        for memory in entity.memories() {
            memory_indices.push(memory_index(
                store,
                &mut memories,
                &mut memory_keys,
                *memory,
            ));
        }
        let mut global_indices = Vec::with_capacity(entity.globals().len());
        for global in entity.globals() {
            global_indices.push(global_index(store, &mut globals, &mut global_keys, *global));
        }
        instances.push(InstanceImage {
            module_index,
            memories: memory_indices,
            globals: global_indices,
        });
    }

    let frames = stack
        .coredump_frames()
        .into_iter()
        .enumerate()
        .map(|(index, frame)| {
            let at_instr_start = matches!(mode, FrameMode::Trap) && index == 0;
            encode_frame(store, stack, code, &frame, &instance_ptrs, at_instr_start)
        })
        .collect();

    Captured {
        modules: modules.into_iter().map(|(_, name)| name).collect(),
        instances,
        memories,
        globals,
        frames,
    }
}

fn module_index(modules: &mut Vec<(usize, String)>, entity: &InstanceEntity) -> u32 {
    let id = entity.module_id();
    if let Some(index) = modules.iter().position(|(key, _)| *key == id) {
        return u32::try_from(index).unwrap_or(u32::MAX);
    }
    let index = modules.len();
    modules.push((id, entity.module_name().into()));
    u32::try_from(index).unwrap_or(u32::MAX)
}

fn memory_index<T>(
    store: &Store<T>,
    memories: &mut Vec<MemoryImage>,
    keys: &mut Vec<Memory>,
    memory: Memory,
) -> u32 {
    if let Some(index) = keys
        .iter()
        .position(|candidate| candidate.as_raw() == memory.as_raw())
    {
        return u32::try_from(index).unwrap_or(u32::MAX);
    }
    let ty = memory.ty(store);
    let current = memory.size(store);
    let maximum = ty.maximum().map(|max| max.max(current));
    memories.push(MemoryImage {
        is_64: ty.is_64(),
        minimum: current,
        maximum,
        page_size_log2: ty.page_size_log2(),
        bytes: memory.data(store).to_vec(),
    });
    let index = keys.len();
    keys.push(memory);
    u32::try_from(index).unwrap_or(u32::MAX)
}

fn global_index<T>(
    store: &Store<T>,
    globals: &mut Vec<GlobalImage>,
    keys: &mut Vec<Global>,
    global: Global,
) -> u32 {
    if let Some(index) = keys
        .iter()
        .position(|candidate| candidate.as_raw() == global.as_raw())
    {
        return u32::try_from(index).unwrap_or(u32::MAX);
    }
    let ty = global.ty(store);
    globals.push(GlobalImage {
        ty: ty.content(),
        mutable: matches!(ty.mutability(), Mutability::Var),
        value: global.get(store),
    });
    let index = keys.len();
    keys.push(global);
    u32::try_from(index).unwrap_or(u32::MAX)
}

fn encode_frame<T>(
    _store: &Store<T>,
    stack: &Stack,
    code: &CodeMap,
    frame: &CoredumpFrame,
    instances: &[*const InstanceEntity],
    at_instr_start: bool,
) -> Vec<u8> {
    let instance_ptr = unsafe { frame.instance.as_ref() as *const InstanceEntity };
    let instance_index = instances
        .iter()
        .position(|ptr| *ptr == instance_ptr)
        .and_then(|index| u32::try_from(index).ok())
        .unwrap_or(0);

    let resolved = code.with_debug_at(frame.ip.as_ptr(), |ir_offset, debug| {
        resolve_debug(debug, ir_offset, at_instr_start)
    });
    let (func_index, wasm_offset, local_types, local_offsets, operands) = resolved.unwrap_or((
        0,
        0,
        Vec::new(),
        Vec::new(),
        Vec::new(),
    ));

    let mut buf = Vec::new();
    buf.push(0x00);
    write_uleb(&mut buf, instance_index);
    write_uleb(&mut buf, func_index);
    write_uleb(&mut buf, wasm_offset);
    write_uleb(&mut buf, u32::try_from(local_types.len()).unwrap_or(u32::MAX));
    for (ty, offset) in local_types.iter().zip(local_offsets.iter()) {
        encode_slot(&mut buf, stack, frame.start, *offset, *ty);
    }
    write_uleb(&mut buf, u32::try_from(operands.len()).unwrap_or(u32::MAX));
    for operand in operands {
        encode_operand(&mut buf, stack, frame.start, operand);
    }
    buf
}

fn resolve_debug(
    debug: Option<&FuncDebugInfo>,
    ir_offset: u32,
    at_instr_start: bool,
) -> (u32, u32, Vec<ValType>, Vec<u16>, Vec<DebugOperand>) {
    let Some(debug) = debug else {
        return (0, 0, Vec::new(), Vec::new(), Vec::new());
    };
    let site = debug.site_for(ir_offset, at_instr_start);
    (
        debug.func_index,
        site.map(|site| site.wasm_offset).unwrap_or(0),
        debug.local_types.to_vec(),
        debug.local_offsets.to_vec(),
        site.map(|site| site.operands.to_vec())
            .unwrap_or_default(),
    )
}

fn encode_operand(buf: &mut Vec<u8>, stack: &Stack, frame_start: usize, operand: DebugOperand) {
    match operand {
        DebugOperand::Slot { offset, ty } => encode_slot(buf, stack, frame_start, offset, ty),
        DebugOperand::ImmI32(value) => {
            buf.push(0x7F);
            write_sleb(buf, i64::from(value));
        }
        DebugOperand::ImmI64(value) => {
            buf.push(0x7E);
            write_sleb(buf, value);
        }
        DebugOperand::ImmF32(bits) => {
            buf.push(0x7D);
            buf.extend_from_slice(&bits.to_le_bytes());
        }
        DebugOperand::ImmF64(bits) => {
            buf.push(0x7C);
            buf.extend_from_slice(&bits.to_le_bytes());
        }
        DebugOperand::Missing => buf.push(0x01),
    }
}

fn encode_slot(buf: &mut Vec<u8>, stack: &Stack, frame_start: usize, offset: u16, ty: ValType) {
    let Some(index) = frame_start.checked_add(usize::from(offset)) else {
        buf.push(0x01);
        return;
    };
    let Some(cell) = stack.cell(index) else {
        buf.push(0x01);
        return;
    };
    match ty {
        ValType::I32 => {
            buf.push(0x7F);
            write_sleb(buf, i64::from(i32::from(cell)));
        }
        ValType::I64 => {
            buf.push(0x7E);
            write_sleb(buf, i64::from(cell));
        }
        ValType::F32 => {
            buf.push(0x7D);
            let value = f32::from(cell);
            buf.extend_from_slice(&value.to_le_bytes());
        }
        ValType::F64 => {
            buf.push(0x7C);
            let value = f64::from(cell);
            buf.extend_from_slice(&value.to_le_bytes());
        }
        ValType::FuncRef | ValType::ExternRef | ValType::V128 => buf.push(0x01),
    }
}

fn encode(
    executable: &str,
    modules: &[String],
    instances: &[InstanceImage],
    memories: &[MemoryImage],
    globals: &[GlobalImage],
    frames: &[Vec<u8>],
) -> Vec<u8> {
    let mut module = Vec::new();
    module.extend_from_slice(b"\0asm");
    module.extend_from_slice(&1u32.to_le_bytes());

    if !memories.is_empty() {
        let mut payload = Vec::new();
        write_uleb(&mut payload, memories.len() as u32);
        for memory in memories {
            encode_memory_type(&mut payload, memory);
        }
        write_section(&mut module, 5, &payload);
    }
    if !globals.is_empty() {
        let mut payload = Vec::new();
        write_uleb(&mut payload, globals.len() as u32);
        for global in globals {
            encode_global(&mut payload, global);
        }
        write_section(&mut module, 6, &payload);
    }
    if !memories.is_empty() {
        let mut payload = Vec::new();
        write_uleb(&mut payload, memories.len() as u32);
        for (index, memory) in memories.iter().enumerate() {
            encode_data_segment(&mut payload, index as u32, memory);
        }
        write_section(&mut module, 11, &payload);
    }

    write_custom(&mut module, "core", &{
        let mut payload = vec![0x00];
        write_name(&mut payload, executable);
        payload
    });
    write_custom(&mut module, "coremodules", &{
        let mut payload = Vec::new();
        write_uleb(&mut payload, modules.len() as u32);
        for name in modules {
            payload.push(0x00);
            write_name(&mut payload, name);
        }
        payload
    });
    write_custom(&mut module, "coreinstances", &{
        let mut payload = Vec::new();
        write_uleb(&mut payload, instances.len() as u32);
        for instance in instances {
            payload.push(0x00);
            write_uleb(&mut payload, instance.module_index);
            write_uleb(&mut payload, instance.memories.len() as u32);
            for index in &instance.memories {
                write_uleb(&mut payload, *index);
            }
            write_uleb(&mut payload, instance.globals.len() as u32);
            for index in &instance.globals {
                write_uleb(&mut payload, *index);
            }
        }
        payload
    });
    write_custom(&mut module, "corestack", &{
        let mut payload = vec![0x00];
        write_name(&mut payload, "main");
        write_uleb(&mut payload, frames.len() as u32);
        for frame in frames {
            payload.extend_from_slice(frame);
        }
        payload
    });
    module
}

fn encode_memory_type(buf: &mut Vec<u8>, memory: &MemoryImage) {
    let mut flags = 0u8;
    if memory.maximum.is_some() {
        flags |= 0x01;
    }
    if memory.is_64 {
        flags |= 0x04;
    }
    let custom_page = memory.page_size_log2 != 16;
    if custom_page {
        flags |= 0x08;
    }
    buf.push(flags);
    if memory.is_64 {
        write_uleb64(buf, memory.minimum);
        if let Some(maximum) = memory.maximum {
            write_uleb64(buf, maximum);
        }
    } else {
        write_uleb(buf, memory.minimum as u32);
        if let Some(maximum) = memory.maximum {
            write_uleb(buf, maximum as u32);
        }
    }
    if custom_page {
        buf.push(memory.page_size_log2);
    }
}

fn encode_global(buf: &mut Vec<u8>, global: &GlobalImage) {
    buf.push(valtype_byte(global.ty));
    buf.push(u8::from(global.mutable));
    match &global.value {
        Val::I32(value) => {
            buf.push(0x41);
            write_sleb(buf, i64::from(*value));
        }
        Val::I64(value) => {
            buf.push(0x42);
            write_sleb(buf, *value);
        }
        Val::F32(value) => {
            buf.push(0x43);
            buf.extend_from_slice(&value.to_bits().to_le_bytes());
        }
        Val::F64(value) => {
            buf.push(0x44);
            buf.extend_from_slice(&value.to_bits().to_le_bytes());
        }
        Val::V128(value) => {
            buf.push(0xFD);
            buf.push(0x0C);
            buf.extend_from_slice(&value.as_u128().to_le_bytes());
        }
        Val::FuncRef(_) | Val::ExternRef(_) => {
            // The coredump module has no function or extern definitions.
            // `ref.null` is a valid initializer for reference globals.
            buf.push(0xD0);
            buf.push(valtype_byte(global.ty));
        }
    }
    buf.push(0x0B);
}

fn valtype_byte(ty: ValType) -> u8 {
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

fn encode_data_segment(buf: &mut Vec<u8>, index: u32, memory: &MemoryImage) {
    if index == 0 {
        buf.push(0x00);
    } else {
        buf.push(0x02);
        write_uleb(buf, index);
    }
    if memory.is_64 {
        buf.push(0x42);
        write_sleb(buf, 0);
    } else {
        buf.push(0x41);
        write_sleb(buf, 0);
    }
    buf.push(0x0B);
    write_uleb(buf, memory.bytes.len() as u32);
    buf.extend_from_slice(&memory.bytes);
}

fn write_section(module: &mut Vec<u8>, id: u8, payload: &[u8]) {
    module.push(id);
    write_uleb64(module, payload.len() as u64);
    module.extend_from_slice(payload);
}

fn write_custom(module: &mut Vec<u8>, name: &str, content: &[u8]) {
    let mut payload = Vec::new();
    write_name(&mut payload, name);
    payload.extend_from_slice(content);
    write_section(module, 0, &payload);
}

fn write_name(buf: &mut Vec<u8>, name: &str) {
    write_uleb(buf, name.len() as u32);
    buf.extend_from_slice(name.as_bytes());
}

fn write_uleb(buf: &mut Vec<u8>, value: u32) {
    write_uleb64(buf, u64::from(value));
}

fn write_uleb64(buf: &mut Vec<u8>, mut value: u64) {
    loop {
        let mut byte = (value as u8) & 0x7F;
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

/// Pulls already-encoded stack frames out of a coredump image.
fn extract_frames(bytes: &[u8]) -> Option<Vec<Vec<u8>>> {
    if bytes.len() < 8 || &bytes[..4] != b"\0asm" {
        return None;
    }
    let mut index = 8;
    while index < bytes.len() {
        let id = *bytes.get(index)?;
        index += 1;
        let (size, next) = read_uleb(bytes, index)?;
        index = next;
        let size = usize::try_from(size).ok()?;
        let payload = bytes.get(index..index.checked_add(size)?)?;
        index += size;
        if id != 0 {
            continue;
        }
        let (name_len, next) = read_uleb(payload, 0)?;
        let name_len = usize::try_from(name_len).ok()?;
        let name = payload.get(next..next.checked_add(name_len)?)?;
        let content = payload.get(next + name_len..)?;
        if name == b"corestack" {
            return parse_corestack(content);
        }
    }
    None
}

fn parse_corestack(content: &[u8]) -> Option<Vec<Vec<u8>>> {
    if content.first().copied() != Some(0x00) {
        return None;
    }
    let (name_len, mut index) = read_uleb(content, 1)?;
    let name_len = usize::try_from(name_len).ok()?;
    index = index.checked_add(name_len)?;
    let (count, mut index) = read_uleb(content, index)?;
    let count = usize::try_from(count).ok()?;
    let mut frames = Vec::with_capacity(count);
    for _ in 0..count {
        let start = index;
        index = parse_frame(content, index)?;
        frames.push(content[start..index].to_vec());
    }
    Some(frames)
}

fn parse_frame(data: &[u8], index: usize) -> Option<usize> {
    if data.get(index).copied() != Some(0x00) {
        return None;
    }
    let (_, index) = read_uleb(data, index + 1)?;
    let (_, index) = read_uleb(data, index)?;
    let (_, index) = read_uleb(data, index)?;
    let index = parse_values(data, index)?;
    parse_values(data, index)
}

fn parse_values(data: &[u8], index: usize) -> Option<usize> {
    let (count, mut index) = read_uleb(data, index)?;
    for _ in 0..count {
        index = parse_value(data, index)?;
    }
    Some(index)
}

fn parse_value(data: &[u8], index: usize) -> Option<usize> {
    let tag = *data.get(index)?;
    let index = index + 1;
    match tag {
        0x01 => Some(index),
        0x7F | 0x7E => skip_sleb(data, index),
        0x7D => index.checked_add(4),
        0x7C => index.checked_add(8),
        _ => None,
    }
}

fn skip_sleb(data: &[u8], mut index: usize) -> Option<usize> {
    for _ in 0..10 {
        let byte = *data.get(index)?;
        index += 1;
        if byte & 0x80 == 0 {
            return Some(index);
        }
    }
    None
}

fn read_uleb(data: &[u8], mut index: usize) -> Option<(u64, usize)> {
    let mut result = 0u64;
    let mut shift = 0;
    loop {
        let byte = *data.get(index)?;
        index += 1;
        result |= u64::from(byte & 0x7F) << shift;
        if byte & 0x80 == 0 {
            return Some((result, index));
        }
        shift += 7;
        if shift > 63 {
            return None;
        }
    }
}
