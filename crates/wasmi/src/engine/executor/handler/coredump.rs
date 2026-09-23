use crate::{
    ValType,
    coredump::{CoreDump, CoreFrame, CoreValue},
    engine::{
        CodeMap,
        executor::handler::{dispatch::ExecutionOutcome, state::Stack},
    },
    func::FuncEntity,
    store::StoreInner,
};
use alloc::{boxed::Box, vec::Vec};

/// Attaches or extends a Wasm coredump for the error of `outcome` if coredumps are enabled.
///
/// - A new coredump is created for Wasm traps.
/// - An existing coredump, e.g. created by a trap in a re-entrant Wasm execution,
///   is extended with the frames of `stack`.
#[cold]
#[inline(never)]
pub fn attach_coredump(
    store: &StoreInner,
    stack: &Stack,
    code: &CodeMap,
    outcome: &mut ExecutionOutcome,
) {
    let config = store.engine().config();
    if !config.get_generate_coredump() {
        return;
    }
    let (error, may_create) = match outcome {
        ExecutionOutcome::Error(error) => (error, true),
        ExecutionOutcome::Host(error) => (error.host_error_mut(), false),
        ExecutionOutcome::OutOfFuel(_) => return,
    };
    let mut coredump = match error.take_coredump() {
        Some(coredump) => coredump,
        None if may_create && error.as_trap_code().is_some() => {
            Box::new(CoreDump::new(config.get_coredump_executable_name()))
        }
        None => return,
    };
    record_frames(store, stack, code, &mut coredump);
    coredump.encode();
    error.set_coredump(coredump);
}

/// Records all Wasm frames of `stack` from youngest to oldest into `coredump`.
fn record_frames(store: &StoreInner, stack: &Stack, code: &CodeMap, coredump: &mut CoreDump) {
    for (ip, start, inst) in stack.coredump_frames() {
        // Safety: the instance is kept alive by the store during execution.
        let entity = unsafe { inst.as_ref() };
        let ip = ip.addr();
        let mut found = None;
        let mut func_index = 0;
        while let Some(func) = entity.get_func(func_index) {
            if let FuncEntity::Wasm(wasm_func) = store.resolve_func(&func) {
                if let Some(cref) = code.get_compiled(wasm_func.func_body()) {
                    let ops = cref.ops();
                    let begin = ops.as_ptr() as usize;
                    if (begin..begin + ops.len()).contains(&ip) {
                        found = Some((func_index, cref.local_types()));
                    }
                }
            }
            func_index += 1;
        }
        let Some((func, local_types)) = found else {
            continue;
        };
        let key = entity as *const _ as usize;
        let instance = match coredump.find_instance(key) {
            Some(instance) => instance,
            None => record_instance(store, entity, key, coredump),
        };
        let mut offset = start;
        let mut locals = Vec::with_capacity(local_types.len());
        for &ty in local_types {
            let value = match stack.coredump_cell(offset) {
                Some(bits) => CoreValue::from_bits(ty, bits),
                None => CoreValue::Missing(ty),
            };
            locals.push(value);
            offset += match ty {
                ValType::V128 => 2,
                _ => 1,
            };
        }
        coredump.push_frame(CoreFrame {
            instance,
            func,
            locals,
        });
    }
}

/// Records the linear memories and global variables of the instance `entity`.
fn record_instance(
    store: &StoreInner,
    entity: &crate::instance::InstanceEntity,
    key: usize,
    coredump: &mut CoreDump,
) -> u32 {
    let instance = coredump.push_instance(key);
    let mut index = 0;
    while let Some(memory) = entity.get_memory(index) {
        let memory = store.resolve_memory(&memory);
        let key = memory as *const _ as usize;
        coredump.add_memory(instance, key, || {
            (memory.dynamic_ty(), Box::from(memory.data()))
        });
        index += 1;
    }
    let mut index = 0;
    while let Some(global) = entity.get_global(index) {
        let global = store.resolve_global(&global);
        let key = global as *const _ as usize;
        let ty = global.ty();
        let raw = *global.get_raw();
        let value = match ty.content() {
            ValType::I32 => CoreValue::I32(i32::from(raw)),
            ValType::I64 => CoreValue::I64(i64::from(raw)),
            ValType::F32 => CoreValue::F32(u32::from(raw)),
            ValType::F64 => CoreValue::F64(u64::from(raw)),
            #[cfg(feature = "simd")]
            ValType::V128 => CoreValue::V128(crate::V128::from(raw).as_u128()),
            ty => CoreValue::Missing(ty),
        };
        let mutable = matches!(ty.mutability(), crate::Mutability::Var);
        coredump.add_global(instance, key, mutable, value);
        index += 1;
    }
    instance
}
