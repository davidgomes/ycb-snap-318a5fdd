"""State-owned data lifecycle, scoping, history, and API tests."""

import pickle

import pytest
from statemachine.exceptions import InvalidDefinition
from statemachine.io.scxml.parser import parse_scxml
from statemachine.io.scxml.processor import SCXMLProcessor

from statemachine import DataChangeInfo
from statemachine import DataVar
from statemachine import HistoryState
from statemachine import State
from statemachine import StateChart


class Counter:
    value = 0


def next_counter():
    Counter.value += 1
    return Counter.value


class BasicDataMachine(StateChart):
    idle = State(initial=True, data={"count": 0, "tags": ["a"]})
    working = State(data={"count": DataVar(default=0, type=int)})

    start = idle.to(working)
    stop = working.to(idle)


class ParallelDataMachine(StateChart):
    class regions(State.Parallel):
        class region_a(State.Compound):
            a1 = State(initial=True, data={"a": 1})

        class region_b(State.Compound):
            b1 = State(initial=True, data={"b": 2})

        reset_a = region_a.a1.to.itself()


class HistoryDataMachine(StateChart):
    outside = State(initial=True)

    class compound(State.Compound):
        s1 = State(initial=True, data={"value": 0})
        s2 = State(data={"value": 99})
        h = HistoryState(type="shallow")

        go_s2 = s1.to(s2)
        go_s1 = s2.to(s1)

    enter = outside.to(compound)
    leave = compound.to(outside)
    return_via_history = outside.to(compound.h)


@pytest.mark.timeout(5)
class TestStateDataLifecycle:
    async def test_data_initialized_on_entry_and_removed_on_exit(self, sm_runner):
        sm = await sm_runner.start(BasicDataMachine)
        assert sm.get_state_data(sm.idle) == {"count": 0, "tags": ["a"]}

        await sm_runner.send(sm, "start")
        assert sm.get_state_data(sm.working) == {"count": 0}

        await sm_runner.send(sm, "stop")
        assert sm.get_state_data(sm.working) is None
        assert sm.get_state_data(sm.idle) == {"count": 0, "tags": ["a"]}

    async def test_reentry_resets_defaults(self, sm_runner):
        sm = await sm_runner.start(BasicDataMachine)
        await sm_runner.send(sm, "start")
        sm.set_state_data(sm.working, "count", 5)
        await sm_runner.send(sm, "stop")
        await sm_runner.send(sm, "start")
        assert sm.get_state_data(sm.working) == {"count": 0}

    async def test_factory_and_callable_defaults(self, sm_runner):
        Counter.value = 0

        class FactoryMachine(StateChart):
            s1 = State(initial=True, data={"n": DataVar(factory=next_counter), "m": next_counter})
            s2 = State()

            go = s1.to(s2)
            back = s2.to(s1)

        sm = await sm_runner.start(FactoryMachine)
        first = sm.get_state_data(sm.s1)["n"]
        second = sm.get_state_data(sm.s1)["m"]
        assert first != second

        await sm_runner.send(sm, "go")
        await sm_runner.send(sm, "back")
        restored = sm.get_state_data(sm.s1)
        assert restored["n"] != first
        assert restored["m"] != second


@pytest.mark.timeout(5)
class TestStateDataCallbacks:
    async def test_state_data_injected_in_callbacks(self, sm_runner):
        seen = {}

        class CallbackMachine(StateChart):
            s1 = State(initial=True, data={"x": 1})
            s2 = State(data={"y": 2})

            def on_enter_s2(self, state_data):
                seen["enter"] = dict(state_data)

            def on_exit_s1(self, state_data):
                seen["exit"] = dict(state_data)

            def on_start(self, state_data):
                seen["on"] = dict(state_data)

            start = s1.to(s2, on="on_start")
            stay = s2.to.itself()

        sm = await sm_runner.start(CallbackMachine)
        await sm_runner.send(sm, "start")
        assert seen["exit"] == {"x": 1}
        assert seen["on"] == {"x": 1}
        assert seen["enter"] == {"y": 2}

    async def test_hierarchical_scoping_shadows_parent(self, sm_runner):
        captured = {}

        class ShadowMachine(StateChart):
            class parent(State.Compound, data={"key": "root", "only_root": 2}):
                class child(State.Compound, data={"key": "child", "only_parent": 1}):
                    leaf = State(initial=True, data={"key": "leaf"})

                    def on_enter_leaf(self, state_data):
                        captured["enter"] = dict(state_data)

        await sm_runner.start(ShadowMachine)
        assert captured["enter"] == {"key": "leaf", "only_parent": 1, "only_root": 2}

    async def test_parallel_regions_isolate_scopes(self, sm_runner):
        captured = {}

        class IsoMachine(StateChart):
            class regions(State.Parallel):
                class region_a(State.Compound):
                    a = State(initial=True, data={"secret": "a"})

                class region_b(State.Compound):
                    b = State(initial=True, data={"secret": "b"})

                    def on_enter_b(self, state_data):
                        captured["b"] = dict(state_data)

        sm = await sm_runner.start(IsoMachine)
        assert captured["b"] == {"secret": "b"}
        assert "secret" in sm.get_scoped_state_data(sm.regions.region_b.b)


@pytest.mark.timeout(5)
class TestStateDataAPI:
    async def test_state_data_values_snapshot(self, sm_runner):
        sm = await sm_runner.start(ParallelDataMachine)
        values = sm.state_data_values
        assert "a1" in values
        assert "b1" in values
        assert values["a1"]["a"] == 1
        assert values["b1"]["b"] == 2

    async def test_set_state_data_and_changes(self, sm_runner):
        sm = await sm_runner.start(BasicDataMachine)
        await sm_runner.send(sm, "start")
        sm.set_state_data(sm.working, "count", 3)
        changes = sm.get_data_changes()
        assert len(changes) == 1
        assert changes[0] == DataChangeInfo("working", "count", 0, 3)

        await sm_runner.send(sm, "stop")
        assert sm.get_data_changes() == []

    async def test_set_state_data_validations(self, sm_runner):
        sm = await sm_runner.start(BasicDataMachine)
        await sm_runner.send(sm, "start")

        with pytest.raises(InvalidDefinition, match="inactive"):
            sm.set_state_data(sm.idle, "count", 1)

        with pytest.raises(InvalidDefinition, match="not declared"):
            sm.set_state_data(sm.working, "missing", 1)

        with pytest.raises(InvalidDefinition, match="type"):
            sm.set_state_data(sm.working, "count", "bad")


class TestStateDataValidation:
    def test_invalid_data_schema(self):
        with pytest.raises(InvalidDefinition, match="dict"):
            State(initial=True, data=["bad"])  # type: ignore[arg-type]

        with pytest.raises(InvalidDefinition, match="string keys"):
            State(initial=True, data={1: 0})  # type: ignore[arg-type]

    def test_datavar_default_and_factory(self):
        with pytest.raises(InvalidDefinition, match="default.*factory"):
            DataVar(1, factory=list)


@pytest.mark.timeout(5)
class TestStateDataHistory:
    async def test_history_restores_data_snapshot(self, sm_runner):
        sm = await sm_runner.start(HistoryDataMachine)
        await sm_runner.send(sm, "enter")
        await sm_runner.send(sm, "go_s2")
        sm.set_state_data(sm.compound.s2, "value", 42)

        await sm_runner.send(sm, "leave")
        assert "h" in sm.history_data_values

        await sm_runner.send(sm, "return_via_history")
        assert sm.get_state_data(sm.compound.s2) == {"value": 42}


class TestStateDataPickle:
    def test_state_data_survives_pickle(self):
        sm = BasicDataMachine()
        sm.send("start")
        sm.set_state_data(sm.working, "count", 7)

        restored = pickle.loads(pickle.dumps(sm))
        assert restored.get_state_data(restored.working) == {"count": 7}
        assert restored.state_data_values["working"]["count"] == 7


class TestStateDataCompoundKeyword:
    def test_compound_accepts_data_keyword(self):
        class CompoundDataMachine(StateChart):
            class box(State.Compound, data={"box_level": 1}):
                inner = State(initial=True, data={"inner_level": 2})

        sm = CompoundDataMachine()
        assert sm.get_state_data(sm.box) == {"box_level": 1}
        assert sm.get_state_data(sm.box.inner) == {"inner_level": 2}


class TestStateDataSCXML:
    def test_state_data_elements_parsed_as_literals(self):
        scxml = """
        <scxml initial="s0" version="1.0">
          <state id="s0">
            <data id="count" expr="3"/>
            <data id="label" expr="'hello'"/>
          </state>
        </scxml>
        """
        definition = parse_scxml(scxml)
        assert definition.states["s0"].data == {"count": 3, "label": "hello"}

    def test_scxml_state_data_runtime(self):
        scxml = """
        <scxml initial="s0" version="1.0">
          <state id="s0">
            <data id="count" expr="1"/>
            <transition event="inc" target="s0"/>
          </state>
        </scxml>
        """
        processor = SCXMLProcessor()
        processor.parse_scxml("Counter", scxml)
        sm_class = processor.scs["Counter"]
        sm = sm_class()
        assert sm.get_state_data(sm.s0) == {"count": 1}


class TestStateDataDiagram:
    def test_diagram_annotates_data_variables(self):
        dot = f"{BasicDataMachine():dot}"
        assert "count" in dot
        assert "tags" in dot
