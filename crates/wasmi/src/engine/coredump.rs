//! Capture Wasm stack, memory, and global state into a coredump.

use super::executor::Inst;
use super::{CodeMap, Stack};
use crate::{
    Error, TrapCode, ValType,
    coredump::{CapturedFrame, CapturedGlobal, CapturedMemory, CoreVal, Coredump},
    store::PrunedStore,
};
use alloc::{boxed::Box, vec::Vec};

/// Attaches a fresh coredump to `error` when generation is enabled.
pub(crate) fn attach_trap(store: &PrunedStore, stack: &Stack, code: &CodeMap, error: &mut Error) {
    if !enabled(store) {
        return;
    }
    let name = store
        .inner()
        .engine()
        .config()
        .get_coredump_executable_name();
    let frames = capture_frames(store, stack, code);
    error.set_coredump(Coredump::new(name, frames));
}

/// Extends an existing coredump with frames from an outer Wasm invocation.
pub(crate) fn extend_trap(store: &PrunedStore, stack: &Stack, code: &CodeMap, error: &mut Error) {
    if !enabled(store) {
        return;
    }
    let Some(coredump) = error.coredump_mut() else {
        return;
    };
    let frames = capture_frames(store, stack, code);
    coredump.extend_frames(frames);
}

/// Builds a trap error and attaches a coredump when generation is enabled.
pub(crate) fn trap_error(
    store: &PrunedStore,
    stack: &Stack,
    code: &CodeMap,
    trap: TrapCode,
) -> Error {
    let mut error = Error::from(trap);
    attach_trap(store, stack, code, &mut error);
    error
}

fn enabled(store: &PrunedStore) -> bool {
    store.inner().engine().config().get_generate_coredump()
}

fn capture_frames(store: &PrunedStore, stack: &Stack, code: &CodeMap) -> Vec<CapturedFrame> {
    let mut frames = Vec::new();
    for (ip, start, instance) in stack.wasm_frames_youngest_first() {
        let Some(info) = code.coredump_info_for_ip(ip.as_ptr()) else {
            continue;
        };
        let entity = unsafe { instance.as_ref() };
        let mut locals = Vec::with_capacity(info.local_tys.len());
        for (ty, offset) in info.local_tys.iter().zip(info.local_offsets.iter()) {
            let index = start.saturating_add(usize::from(*offset));
            locals.push(local_value(stack.cell(index), *ty));
        }
        frames.push(CapturedFrame {
            instance_ptr: instance_ptr(instance),
            module_name: entity.module_name().into(),
            func_index: info.func_index,
            code_offset: 0,
            locals,
            memories: capture_memories(store, entity),
            globals: capture_globals(store, entity),
        });
    }
    frames
}

fn instance_ptr(instance: Inst) -> usize {
    // Inst is a pointer newtype. Reconstruct via the reference address.
    let entity = unsafe { instance.as_ref() };
    entity as *const _ as usize
}

fn local_value(cell: Option<crate::engine::executor::Cell>, ty: ValType) -> CoreVal {
    let Some(cell) = cell else {
        return CoreVal::Missing;
    };
    let bits = u64::from(cell);
    match ty {
        ValType::I32 => CoreVal::I32(bits as i32),
        ValType::I64 => CoreVal::I64(bits as i64),
        ValType::F32 => CoreVal::F32(bits as u32),
        ValType::F64 => CoreVal::F64(bits),
        _ => CoreVal::Missing,
    }
}

fn capture_memories(
    store: &PrunedStore,
    entity: &crate::instance::InstanceEntity,
) -> Vec<CapturedMemory> {
    let mut memories = Vec::new();
    for memory in entity.memories() {
        let resolved = store.inner().resolve_memory(memory);
        let ty = resolved.ty();
        let page_size = usize::try_from(ty.page_size()).unwrap_or(65_536);
        let data = resolved.data();
        let current_pages = if page_size == 0 {
            0
        } else {
            (data.len() / page_size) as u64
        };
        let maximum = ty.maximum().map(|max| max.max(current_pages));
        memories.push(CapturedMemory {
            id: *memory,
            memory64: ty.is_64(),
            minimum: current_pages,
            maximum,
            page_size_log2: ty.page_size_log2(),
            bytes: Box::<[u8]>::from(data),
        });
    }
    memories
}

fn capture_globals(
    store: &PrunedStore,
    entity: &crate::instance::InstanceEntity,
) -> Vec<CapturedGlobal> {
    let mut globals = Vec::new();
    for global in entity.globals() {
        let resolved = store.inner().resolve_global(global);
        let ty = resolved.ty();
        let raw = resolved.get();
        let value = core_val(ty.content(), raw.raw());
        globals.push(CapturedGlobal {
            id: *global,
            val_type: ty.content(),
            mutable: ty.mutability().is_mut(),
            value,
        });
    }
    globals
}

fn core_val(ty: ValType, raw: crate::core::RawVal) -> CoreVal {
    use crate::core::ReadAs;
    match ty {
        ValType::I32 => CoreVal::I32(raw.read_as()),
        ValType::I64 => CoreVal::I64(raw.read_as()),
        ValType::F32 => CoreVal::F32(f32::to_bits(raw.read_as())),
        ValType::F64 => CoreVal::F64(f64::to_bits(raw.read_as())),
        ValType::FuncRef => CoreVal::FuncRef,
        ValType::ExternRef => CoreVal::ExternRef,
        #[cfg(feature = "simd")]
        ValType::V128 => {
            let value: crate::V128 = raw.read_as();
            let _ = value;
            CoreVal::Missing
        }
        #[cfg(not(feature = "simd"))]
        ValType::V128 => CoreVal::Missing,
    }
}
