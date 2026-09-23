import pickle

import pytest
from statemachine.contrib.diagram import DotGraphMachine
from statemachine.exceptions import InvalidDefinition
from statemachine.io.scxml.processor import SCXMLProcessor

from statemachine import DataChangeInfo
from statemachine import DataVar
from statemachine import HistoryState
from statemachine import State
from statemachine import StateChart


class Counter(StateChart):
    idle = State(initial=True)
    counting = State(data={"count": 0, "items": list, "tag": DataVar("x", type=str)})

    start = idle.to(counting)
    stop = counting.to(idle)
    loop = counting.to.itself()

    def on_enter_counting(self, state_data):
        state_data["count"] += 1
        state_data["items"].append("enter")

    def on_exit_counting(self, state_data):
        self.exit_seen = dict(state_data)


class Nested(StateChart):
    class outer(State.Compound, data={"a": 1, "shared": "outer"}):
        inner = State(initial=True, data={"b": 2, "shared": "inner"})
        other = State()
        h = HistoryState(type="deep")
        move = inner.to(other)

    away = State(final=False)
    leave = outer.to(away)
    back = away.to(outer.h)

    def on_enter_inner(self, state_data):
        self.seen = (state_data["a"], state_data["b"], state_data["shared"])


class Regions(StateChart):
    class p(State.Parallel):
        class r1(State.Compound):
            x = State(initial=True, data={"v": "x"})

        class r2(State.Compound):
            y = State(initial=True, data={"w": "y"})

    def on_enter_y(self, state_data):
        self.y_keys = set(state_data)


async def test_lifecycle_and_reset(sm_runner):
    sm = await sm_runner.start(Counter)
    assert sm.get_state_data("counting") is None
    await sm_runner.send(sm, "start")
    assert sm.get_state_data(sm.counting) == {"count": 1, "items": ["enter"], "tag": "x"}
    await sm_runner.send(sm, "loop")
    assert sm.exit_seen["count"] == 1
    assert sm.get_state_data("counting")["count"] == 1
    await sm_runner.send(sm, "stop")
    assert sm.get_state_data("counting") is None
    assert sm.state_data_values == {}
    assert Counter.counting._data_spec["count"].default == 0


async def test_hierarchy_shadowing_and_parallel_isolation(sm_runner):
    sm = await sm_runner.start(Nested)
    assert sm.seen == (1, 2, "inner")
    sm2 = await sm_runner.start(Regions)
    assert sm2.y_keys == {"w"}


async def test_deep_history_restores_data(sm_runner):
    sm = await sm_runner.start(Nested)
    sm.set_state_data("inner", "b", 42)
    await sm_runner.send(sm, "leave")
    assert sm.get_state_data("inner") is None
    await sm_runner.send(sm, "back")
    assert sm.get_state_data("inner")["b"] == 42
    assert sm.get_state_data("outer")["a"] == 1


def test_set_state_data_validation_and_changes():
    sm = Counter()
    with pytest.raises(InvalidDefinition):
        sm.set_state_data("counting", "count", 1)
    sm.send("start")
    with pytest.raises(InvalidDefinition):
        sm.set_state_data("counting", "missing", 1)
    with pytest.raises(InvalidDefinition):
        sm.set_state_data("counting", "tag", 1)
    sm.set_state_data("counting", "count", 10)
    assert sm.get_data_changes() == [DataChangeInfo("counting", "count", 1, 10)]
    sm.send("loop")
    assert sm.get_data_changes() == []


def test_invalid_declarations():
    with pytest.raises(InvalidDefinition):
        State(data=[1])
    with pytest.raises(InvalidDefinition):
        State(data={1: 2})
    with pytest.raises(InvalidDefinition):
        DataVar(1, factory=list)
    with pytest.raises(InvalidDefinition):
        DataVar(factory=1)  # type: ignore[arg-type]
    with pytest.raises(InvalidDefinition):
        DataVar("a", type=int)


def test_factory_type_violation_on_entry():
    class SM(StateChart):
        s = State(initial=True, data={"v": DataVar(factory=lambda: "a", type=int)})
        done = State(final=True)
        go = s.to(done)

    with pytest.raises(InvalidDefinition):
        SM()


def test_datavar_repr_and_describe():
    assert "factory" in repr(DataVar(factory=list))
    assert repr(DataVar(1)) == "DataVar(1, type=None)"
    assert DataVar(factory=list).describe() == "list()"


def test_pickle():
    sm = Counter()
    sm.send("start")
    restored = pickle.loads(pickle.dumps(sm))
    assert restored.get_state_data("counting") == sm.get_state_data("counting")
    restored.send("stop")
    assert restored.get_state_data("counting") is None


def test_scxml_state_datamodel():
    scxml = """
    <scxml xmlns="http://www.w3.org/2005/07/scxml" initial="s1">
      <state id="s1">
        <datamodel>
          <data id="n" expr="5"/>
          <data id="skip" expr="a + b"/>
          <data id="noexpr"/>
        </datamodel>
      </state>
    </scxml>
    """
    processor = SCXMLProcessor()
    processor.parse_scxml("sm", scxml)
    sm = processor.start()
    assert sm.get_state_data("s1") == {"n": 5}


def test_diagram_annotates_data():
    dot = DotGraphMachine(Counter)().to_string()
    assert "count = 0" in dot
    assert "items = list()" in dot
