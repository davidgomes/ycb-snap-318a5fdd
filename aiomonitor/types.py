from __future__ import annotations

import sys
import traceback
from dataclasses import dataclass
from typing import Dict, List, NamedTuple, Optional, Sequence

if sys.version_info >= (3, 11):
    from enum import StrEnum
else:
    from backports.strenum import StrEnum


@dataclass
class FormattedLiveTaskInfo:
    task_id: str
    state: str
    name: str
    coro: str
    created_location: str
    since: str


@dataclass
class FormattedTerminatedTaskInfo:
    task_id: str
    name: str
    coro: str
    started_since: str
    terminated_since: str


class FormatItemTypes(StrEnum):
    HEADER = "header"
    CONTENT = "content"


class FormattedStackItem(NamedTuple):
    type: FormatItemTypes
    content: str


@dataclass
class SnapshotTask:
    task_id: str
    state: str
    name: str
    coro: str
    created_location: str
    since: str
    task_repr: str
    creation_stack: Optional[List[traceback.FrameSummary]]
    task_stack: List[traceback.FrameSummary]
    creation_chain: Sequence["SnapshotTask"]


@dataclass
class Snapshot:
    id: int
    name: Optional[str]
    captured_at: float
    tasks: Dict[str, SnapshotTask]
    terminated_tasks: Dict[str, "TerminatedTaskInfo"]


@dataclass
class SnapshotSummary:
    id: int
    name: Optional[str]
    running_count: int
    terminated_count: int


@dataclass
class FormattedSnapshotDiff:
    added: List[FormattedLiveTaskInfo]
    removed: List[FormattedLiveTaskInfo]
    common: List[FormattedLiveTaskInfo]


@dataclass
class TerminatedTaskInfo:
    id: str
    name: str
    coro: str
    started_at: float
    terminated_at: float
    cancelled: bool
    termination_stack: Optional[List[traceback.FrameSummary]] = None
    canceller_stack: Optional[List[traceback.FrameSummary]] = None
    exc_repr: Optional[str] = None
    persistent: bool = False


@dataclass
class CancellationChain:
    target_id: str
    canceller_id: str
    canceller_stack: Optional[List[traceback.FrameSummary]] = None
