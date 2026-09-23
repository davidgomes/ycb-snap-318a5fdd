"""State-owned data: declaration, lifecycle, scoping, history recall and runtime API."""

import pickle

import pytest
from statemachine.contrib.diagram.extract import extract
from statemachine.contrib.diagram.model import ActionType
from statemachine.exceptions import InvalidDefinition
from statemachine.io import create_machine_class_from_definition
from statemachine.io.scxml.parser import parse_scxml
from statemachine.io.scxml.processor import SCXMLProcessor
from statemachine.state_data import normalize_data

from statemachine import DataChangeInfo
from statemachine import DataVar
from statemachine import HistoryState
from statemachine import State
from statemachine import StateChart
from statemachine import StateMachine


class Counter(StateChart):
    idle = State(initial=True, data={"count": 0, "items": list})
    working = State(data={"progress": DataVar(0, type=int), "log": DataVar(factory=list)})
    done = State(final=True)

    start = idle.to(working)
    stop = working.to(idle)
    finish = working.to(done)
    bump = idle.to.itself(internal=True, on="do_bump")
    restart = idle.to.itself()

    def do_bump(self, state_data):
        state_data["count"] += 1
        state_data["items"].append(state_data["count"])


class Shire(StateChart):
    class hobbiton(State.Compound, data={"name": "Hobbiton", "visitors": 0}):
        bag_end = State(initial=True, data={"name": "Bag End", "rings": 1})
        garden = State(data={"flowers": 3})

        walk = bag_end.to(garden)

    class adventure(State.Parallel, data={"party": 9}):
        class north(State.Compound):
            road = State(initial=True, data={"miles": 0})
            mountain = State(final=True)
            climb = road.to(mountain)

        class south(State.Compound):
            river = State(initial=True, data={"boats": 3})
            sea = State(final=True)
            sail = river.to(sea)

    leave = hobbiton.to(adventure)


class Moria(StateChart):
    outside = State(initial=True)

    class mines(State.Compound, data={"depth": 0}):
        class halls(State.Compound, data={"torches": 2}):
            gate = State(initial=True, data={"knocks": 0})
            chamber = State(data={"orcs": 5})
            explore = gate.to(chamber)

        bridge = State(data={"balrog": True})
        deep = HistoryState(type="deep")
        shallow = HistoryState()
        cross = halls.to(bridge)
        _ = deep.to(halls)

    enter_mines = outside.to(mines)
    escape = mines.to(outside)
    return_deep = outside.to(mines.deep)
    return_shallow = outside.to(mines.shallow)


class TestDeclaration:
    def test_plain_values_become_defaults_and_callables_become_factories(self):
        data = normalize_data({"count": 0, "items": list, "var": DataVar(1)})

        assert data["count"].default == 0
        assert data["count"].factory is None
        assert data["items"].factory is list
        assert data["var"].default == 1

    def test_state_without_data_has_empty_declarations(self):
        assert State().data == {}

    @pytest.mark.parametrize("data", [["count"], "count", 1])
    def test_data_must_be_a_dict(self, data):
        with pytest.raises(InvalidDefinition, match="must be a dict"):
            State(data=data)

    def test_data_keys_must_be_strings(self):
        with pytest.raises(InvalidDefinition, match="keys must be strings"):
            State(data={1: "one"})

    def test_datavar_rejects_default_and_factory(self):
        with pytest.raises(InvalidDefinition, match="either 'default' or 'factory'"):
            DataVar(0, factory=int)

    def test_datavar_rejects_non_callable_factory(self):
        with pytest.raises(InvalidDefinition, match="must be callable"):
            DataVar(factory=1)

    def test_datavar_validates_default_against_type(self):
        with pytest.raises(InvalidDefinition, match="expects a value of type int"):
            DataVar("zero", type=int)

    def test_datavar_without_default_is_none(self):
        assert DataVar(type=int).new_value() is None

    def test_datavar_accepts_tuple_of_types(self):
        var = DataVar(1, type=(int, float))
        var.validate(1.5)
        with pytest.raises(InvalidDefinition, match="int | float"):
            var.validate("x")

    def test_datavar_repr(self):
        assert repr(DataVar(0)) == "DataVar(default=0)"
        assert repr(DataVar(0, type=int)) == "DataVar(default=0, type=int)"
        assert repr(DataVar(factory=list, type=list)) == "DataVar(factory=list, type=list)"

    def test_datavar_describe(self):
        assert DataVar(0).describe("count") == "count = 0"
        assert DataVar("a", type=str).describe("name") == "name: str = 'a'"
        assert DataVar(factory=list).describe("items") == "items = list()"

    def test_data_is_stored_per_instance_not_on_state(self):
        sm1 = Counter()
        sm2 = Counter()
        sm1.bump()

        assert sm1.get_state_data("idle")["count"] == 1
        assert sm2.get_state_data("idle")["count"] == 0
        assert Counter.idle.data["count"].default == 0
        assert not hasattr(Counter.idle.data["count"], "value")

    def test_compound_and_parallel_accept_data_keyword(self):
        assert Shire.hobbiton.data["name"].default == "Hobbiton"
        assert Shire.adventure.data["party"].default == 9
        assert Shire.adventure.parallel


class TestLifecycle:
    async def test_entry_initializes_fresh_copy_of_defaults(self, sm_runner):
        sm = await sm_runner.start(Counter)

        assert sm.get_state_data(sm.idle) == {"count": 0, "items": []}
        assert sm.get_state_data("working") is None

    async def test_exit_removes_data(self, sm_runner):
        sm = await sm_runner.start(Counter)
        await sm_runner.send(sm, "start")

        assert sm.get_state_data("idle") is None
        assert sm.get_state_data("working") == {"progress": 0, "log": []}

    async def test_reentry_resets_to_defaults(self, sm_runner):
        sm = await sm_runner.start(Counter)
        await sm_runner.send(sm, "bump")
        await sm_runner.send(sm, "bump")
        assert sm.get_state_data("idle") == {"count": 2, "items": [1, 2]}

        await sm_runner.send(sm, "start")
        await sm_runner.send(sm, "stop")

        assert sm.get_state_data("idle") == {"count": 0, "items": []}

    async def test_mutable_defaults_are_not_shared(self, sm_runner):
        class Sheet(StateChart):
            s1 = State(initial=True, data={"rows": [[0]]})
            s2 = State()
            go = s1.to(s2) | s2.to(s1)

        sm = await sm_runner.start(Sheet)
        sm.get_state_data("s1")["rows"][0].append(1)
        await sm_runner.send(sm, "go")
        await sm_runner.send(sm, "go")

        assert sm.get_state_data("s1") == {"rows": [[0]]}
        assert Sheet.s1.data["rows"].default == [[0]]

    async def test_external_self_transition_resets_data(self, sm_runner):
        sm = await sm_runner.start(Counter)
        await sm_runner.send(sm, "bump")
        await sm_runner.send(sm, "restart")

        assert sm.get_state_data("idle") == {"count": 0, "items": []}

    async def test_internal_self_transition_keeps_data(self, sm_runner):
        sm = await sm_runner.start(Counter)
        await sm_runner.send(sm, "bump")

        assert sm.get_state_data("idle")["count"] == 1

    async def test_factory_type_is_validated_on_entry(self, sm_runner):
        class Broken(StateChart):
            s1 = State(initial=True, data={"n": DataVar(factory=lambda: "x", type=int)})
            s2 = State(final=True)
            go = s1.to(s2)

        with pytest.raises(InvalidDefinition, match="'n' expects a value of type int"):
            await sm_runner.start(Broken)

    async def test_data_persists_through_enter_and_exit_callbacks(self, sm_runner):
        seen = []

        class Tracker(StateChart):
            s1 = State(initial=True, data={"visits": 0})
            s2 = State()
            go = s1.to(s2)
            back = s2.to(s1)

            def on_enter_s1(self, state_data):
                state_data["visits"] += 1

            def on_exit_s1(self, state_data):
                seen.append(dict(state_data))
                state_data["visits"] += 10
                seen.append(dict(state_data))

        sm = await sm_runner.start(Tracker)
        assert sm.get_state_data("s1") == {"visits": 1}

        await sm_runner.send(sm, "go")
        assert seen == [{"visits": 1}, {"visits": 11}]
        assert sm.get_state_data("s1") is None

    async def test_final_state_keeps_data(self, sm_runner):
        class Quest(StateChart):
            start = State(initial=True)
            end = State(final=True, data={"reward": "ring"})
            go = start.to(end)

        sm = await sm_runner.start(Quest)
        await sm_runner.send(sm, "go")

        assert sm.is_terminated
        assert sm.get_state_data("end") == {"reward": "ring"}

    def test_data_initialized_for_configuration_restored_from_model(self):
        class Model:
            state = "working"

        sm = Counter(model=Model())

        assert sm.get_state_data("working") == {"progress": 0, "log": []}
        assert sm.get_state_data("idle") is None

    def test_data_rolled_back_when_microstep_fails(self):
        class Fragile(StateMachine):
            s1 = State(initial=True, data={"n": 1})
            s2 = State(final=True, data={"m": 2})
            go = s1.to(s2)

            def on_enter_s2(self):
                raise ValueError("boom")

        sm = Fragile()
        with pytest.raises(ValueError, match="boom"):
            sm.go()

        assert sm.state_data_values == {"s1": {"n": 1}}

    def test_data_rolled_back_when_microstep_raises_invalid_definition(self):
        class Fragile(StateChart):
            s1 = State(initial=True, data={"n": 1})
            s2 = State(final=True, data={"m": 2})
            go = s1.to(s2)

            def on_enter_s2(self, state_data):
                state_data["undeclared"] = 1

        sm = Fragile()
        with pytest.raises(InvalidDefinition, match="'undeclared' is not declared"):
            sm.go()

        assert sm.state_data_values == {"s1": {"n": 1}}


class TestCallbackInjection:
    async def test_state_data_in_transition_callbacks(self, sm_runner):
        calls = []

        class Flow(StateChart):
            s1 = State(initial=True, data={"ready": True})
            s2 = State(final=True, data={"value": 7})
            go = s1.to(s2, cond="is_ready", before="log_before", after="log_after")

            def is_ready(self, state_data):
                calls.append(("cond", dict(state_data)))
                return state_data["ready"]

            def log_before(self, state_data, source, target):
                calls.append(("before", dict(state_data), source.id, target.id))

            def log_after(self, state_data, event_data):
                calls.append(("after", dict(state_data), dict(event_data.state_data)))

        sm = await sm_runner.start(Flow)
        await sm_runner.send(sm, "go")

        assert calls == [
            ("cond", {"ready": True}),
            ("before", {"ready": True}, "s1", "s2"),
            ("after", {"value": 7}, {"value": 7}),
        ]

    async def test_enabled_events_inject_state_data(self, sm_runner):
        class Gate(StateChart):
            closed = State(initial=True, data={"key": False})
            opened = State(final=True)
            open = closed.to(opened, cond="has_key")

            def has_key(self, state_data):
                return state_data["key"]

        sm = await sm_runner.start(Gate)
        enabled = sm.enabled_events()
        if sm_runner.is_async:
            enabled = await enabled
        assert enabled == []

        sm.set_state_data("closed", "key", True)
        enabled = sm.enabled_events()
        if sm_runner.is_async:
            enabled = await enabled
        assert [e.id for e in enabled] == ["open"]


class TestScoping:
    async def test_child_sees_ancestor_data_and_shadows_on_collision(self, sm_runner):
        seen = {}

        class Scoped(Shire):
            def on_enter_bag_end(self, state_data):
                seen["bag_end"] = dict(state_data)

            def on_enter_hobbiton(self, state_data):
                seen["hobbiton"] = dict(state_data)

        await sm_runner.start(Scoped)

        assert seen["hobbiton"] == {"name": "Hobbiton", "visitors": 0}
        assert seen["bag_end"] == {"name": "Bag End", "visitors": 0, "rings": 1}

    async def test_writes_route_to_the_owning_state(self, sm_runner):
        class Scoped(Shire):
            def on_exit_bag_end(self, state_data):
                state_data["visitors"] += 1
                state_data["name"] = "Bag End (visited)"

        sm = await sm_runner.start(Scoped)
        await sm_runner.send(sm, "walk")

        assert sm.get_state_data("hobbiton") == {"name": "Hobbiton", "visitors": 1}
        assert sm.get_state_data("garden") == {"flowers": 3}

    async def test_parallel_regions_are_isolated(self, sm_runner):
        seen = {}

        class Scoped(Shire):
            def on_enter_road(self, state_data):
                seen["road"] = dict(state_data)

            def on_enter_river(self, state_data):
                seen["river"] = dict(state_data)

        sm = await sm_runner.start(Scoped)
        await sm_runner.send(sm, "leave")

        assert seen["road"] == {"party": 9, "miles": 0}
        assert seen["river"] == {"party": 9, "boats": 3}
        assert sm.state_data_values == {
            "adventure": {"party": 9},
            "road": {"miles": 0},
            "river": {"boats": 3},
        }

    def test_scoped_mapping_protocol(self):
        captured = {}

        class Scoped(Shire):
            def on_enter_bag_end(self, state_data):
                captured["view"] = state_data

        Scoped()
        view = captured["view"]

        assert len(view) == 3
        assert sorted(view) == ["name", "rings", "visitors"]
        assert "visitors" in view
        assert "flowers" not in view
        assert view.get("missing", "default") == "default"
        assert view == {"name": "Bag End", "rings": 1, "visitors": 0}
        assert repr(view) == "{'name': 'Bag End', 'visitors': 0, 'rings': 1}"
        with pytest.raises(KeyError):
            view["flowers"]

    def test_scoped_mapping_rejects_undeclared_writes_and_deletes(self):
        captured = {}

        class Scoped(Shire):
            def on_enter_bag_end(self, state_data):
                captured["view"] = state_data

        Scoped()
        view = captured["view"]

        with pytest.raises(InvalidDefinition, match="'flowers' is not declared"):
            view["flowers"] = 1
        with pytest.raises(InvalidDefinition, match="cannot be deleted"):
            del view["rings"]

    def test_scoped_mapping_enforces_type(self):
        sm = Counter()
        sm.start()
        view = sm._state_data.scope(Counter.working)

        with pytest.raises(InvalidDefinition, match="'progress' expects a value of type int"):
            view["progress"] = "half"
        view["progress"] = 50
        assert sm.get_state_data("working")["progress"] == 50


class TestHistory:
    async def test_deep_history_restores_all_descendant_data(self, sm_runner):
        sm = await sm_runner.start(Moria)
        await sm_runner.send(sm, "enter_mines")
        await sm_runner.send(sm, "explore")
        sm.set_state_data("mines", "depth", 3)
        sm.set_state_data("halls", "torches", 1)
        sm.set_state_data("chamber", "orcs", 2)

        await sm_runner.send(sm, "escape")
        assert sm.state_data_values == {}

        await sm_runner.send(sm, "return_deep")

        assert sm.state_data_values == {
            "mines": {"depth": 0},
            "halls": {"torches": 1},
            "chamber": {"orcs": 2},
        }

    async def test_shallow_history_restores_direct_children_only(self, sm_runner):
        sm = await sm_runner.start(Moria)
        await sm_runner.send(sm, "enter_mines")
        await sm_runner.send(sm, "explore")
        sm.set_state_data("halls", "torches", 1)
        sm.set_state_data("chamber", "orcs", 2)

        await sm_runner.send(sm, "escape")
        await sm_runner.send(sm, "return_shallow")

        assert sm.state_data_values == {
            "mines": {"depth": 0},
            "halls": {"torches": 1},
            "gate": {"knocks": 0},
        }

    async def test_history_snapshot_includes_exit_callback_changes(self, sm_runner):
        class Tracked(Moria):
            def on_exit_bridge(self, state_data):
                state_data["balrog"] = False

        sm = await sm_runner.start(Tracked)
        await sm_runner.send(sm, "enter_mines")
        await sm_runner.send(sm, "cross")
        await sm_runner.send(sm, "escape")
        await sm_runner.send(sm, "return_shallow")

        assert sm.get_state_data("bridge") == {"balrog": False}

    async def test_restored_data_is_a_copy_of_the_snapshot(self, sm_runner):
        sm = await sm_runner.start(Moria)
        await sm_runner.send(sm, "enter_mines")
        await sm_runner.send(sm, "cross")
        await sm_runner.send(sm, "escape")
        await sm_runner.send(sm, "return_shallow")
        sm.set_state_data("bridge", "balrog", False)
        await sm_runner.send(sm, "escape")
        await sm_runner.send(sm, "enter_mines")

        assert sm.get_state_data("gate") == {"knocks": 0}
        assert sm.get_state_data("bridge") is None
        assert sm._state_data.history_snapshot("shallow") == {"bridge": {"balrog": False}}

    async def test_history_default_entry_uses_defaults(self, sm_runner):
        sm = await sm_runner.start(Moria)
        await sm_runner.send(sm, "return_deep")

        assert sm.state_data_values == {
            "mines": {"depth": 0},
            "halls": {"torches": 2},
            "gate": {"knocks": 0},
        }


class TestRuntimeAPI:
    def test_get_state_data_accepts_state_instance_state_or_id(self):
        sm = Counter()

        assert sm.get_state_data(Counter.idle) == {"count": 0, "items": []}
        assert sm.get_state_data(sm.idle) == {"count": 0, "items": []}
        assert sm.get_state_data("idle") == {"count": 0, "items": []}
        assert sm.get_state_data("unknown") is None

    def test_state_data_values_is_a_snapshot(self):
        sm = Counter()
        values = sm.state_data_values
        values["idle"]["count"] = 99

        assert sm.get_state_data("idle")["count"] == 0

    def test_set_state_data(self):
        sm = Counter()
        sm.set_state_data("idle", "count", 5)
        sm.set_state_data(sm.idle, "items", ["a"])

        assert sm.get_state_data(Counter.idle) == {"count": 5, "items": ["a"]}

    def test_set_state_data_rejects_unknown_state(self):
        with pytest.raises(InvalidDefinition, match="'mordor' is not a valid state"):
            Counter().set_state_data("mordor", "count", 1)

    def test_set_state_data_rejects_inactive_state(self):
        with pytest.raises(InvalidDefinition, match="'working' is not active"):
            Counter().set_state_data("working", "progress", 1)

    def test_set_state_data_rejects_undeclared_key(self):
        with pytest.raises(InvalidDefinition, match="'missing' is not declared on state 'idle'"):
            Counter().set_state_data("idle", "missing", 1)

    def test_set_state_data_rejects_type_violation(self):
        sm = Counter()
        sm.start()

        with pytest.raises(InvalidDefinition, match="'progress' expects a value of type int"):
            sm.set_state_data("working", "progress", "half")
        assert sm.get_state_data("working")["progress"] == 0

    async def test_get_data_changes_cleared_at_each_macrostep(self, sm_runner):
        sm = await sm_runner.start(Counter)
        sm.set_state_data("idle", "count", 10)
        assert sm.get_data_changes() == [DataChangeInfo("idle", "count", 0, 10)]

        await sm_runner.send(sm, "bump")
        changes = sm.get_data_changes()
        assert [(c.state_id, c.key, c.old_value, c.new_value) for c in changes] == [
            ("idle", "count", 10, 11)
        ]

        await sm_runner.send(sm, "start")
        assert sm.get_data_changes() == []

    def test_get_data_changes_returns_a_copy(self):
        sm = Counter()
        sm.set_state_data("idle", "count", 1)
        sm.get_data_changes().clear()

        assert len(sm.get_data_changes()) == 1

    async def test_data_survives_pickle(self, sm_runner):
        sm = await sm_runner.start(Counter)
        await sm_runner.send(sm, "start")
        sm.set_state_data("working", "progress", 42)

        restored = pickle.loads(pickle.dumps(sm))

        assert restored.get_state_data("working") == {"progress": 42, "log": []}
        assert restored.get_data_changes() == [DataChangeInfo("working", "progress", 0, 42)]
        restored.set_state_data("working", "progress", 43)
        assert sm.get_state_data("working")["progress"] == 42


class TestDefinitionsAndSCXML:
    def test_create_machine_class_from_definition_with_data(self):
        sc = create_machine_class_from_definition(
            "Light",
            states={
                "green": {
                    "initial": True,
                    "data": {"ticks": 0},
                    "on": {"go": [{"target": "red"}]},
                },
                "red": {"final": True},
            },
        )

        assert sc().get_state_data("green") == {"ticks": 0}

    def test_scxml_state_datamodel_parsed_as_literals(self):
        definition = parse_scxml(
            """
            <scxml xmlns="http://www.w3.org/2005/07/scxml" initial="s1">
              <datamodel><data id="top" expr="1"/></datamodel>
              <state id="s1">
                <datamodel>
                  <data id="count" expr="0"/>
                  <data id="tags" expr="['a', 'b']"/>
                  <data id="dynamic" expr="top + 1"/>
                  <data id="broken" expr="(("/>
                  <data id="content">text</data>
                </datamodel>
                <transition event="go" target="done"/>
              </state>
              <final id="done"/>
            </scxml>
            """
        )

        assert definition.states["s1"].data == {"count": 0, "tags": ["a", "b"]}
        assert definition.states["done"].data == {}

    def test_scxml_state_data_available_at_runtime(self):
        processor = SCXMLProcessor()
        processor.parse_scxml(
            "counter",
            """
            <scxml xmlns="http://www.w3.org/2005/07/scxml" initial="s1">
              <state id="s1">
                <datamodel><data id="count" expr="3"/></datamodel>
                <transition event="go" target="done"/>
              </state>
              <final id="done"/>
            </scxml>
            """,
        )
        sm = processor.start()

        assert sm.get_state_data("s1") == {"count": 3}
        assert sm.model.count == 3


class TestDiagram:
    def test_extract_annotates_state_data(self):
        graph = extract(Counter)
        idle = next(s for s in graph.states if s.id == "idle")
        working = next(s for s in graph.states if s.id == "working")
        done = next(s for s in graph.states if s.id == "done")

        assert idle.actions[0].type == ActionType.DATA
        assert idle.actions[0].body == "count = 0, items = list()"
        assert working.actions[0].body == "progress: int = 0, log = list()"
        assert done.actions == []

    def test_mermaid_renders_data_annotation(self):
        assert "idle : data / count = 0, items = list()" in f"{Counter:mermaid}"
