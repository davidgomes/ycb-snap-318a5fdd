#[macro_use]
mod dispatch;
#[macro_use]
mod utils;
mod cell;
mod eval;
mod exec;
mod func;
mod state;

pub(crate) fn coredump_trap(state: &mut state::VmState, trap: crate::TrapCode) -> crate::Error {
    crate::engine::coredump::trap_error(state.store, state.stack, state.code, trap)
}

pub(crate) fn coredump_outcome(
    state: &state::VmState,
    outcome: Result<state::Sp, dispatch::ExecutionOutcome>,
) -> Result<state::Sp, dispatch::ExecutionOutcome> {
    match outcome {
        Err(dispatch::ExecutionOutcome::OutOfFuel(mut error)) => {
            let mut trap = crate::Error::from(crate::TrapCode::OutOfFuel);
            crate::engine::coredump::attach_trap(state.store, state.stack, state.code, &mut trap);
            if let Some(coredump) = trap.take_coredump() {
                error.set_coredump(coredump);
            }
            Err(dispatch::ExecutionOutcome::OutOfFuel(error))
        }
        other => other,
    }
}

pub use self::{
    cell::{
        Cell, CellError, CellsReader, CellsWriter, LiftFromCells, LiftFromCellsByValue, LoadByVal,
        LoadFromCellsByValue, LowerToCells, StoreToCells,
    },
    dispatch::{ExecutionOutcome, op_code_to_handler},
    func::{init_host_func_call, init_wasm_func_call, resume_wasm_func_call},
    state::{Inst, Stack},
};
use self::{
    dispatch::{Break, Control, Done},
    state::DoneReason,
};
