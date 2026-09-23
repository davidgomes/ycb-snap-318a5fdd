"""Per-state data ownership: lifecycle, scope, history, SCXML, and diagrams."""

import copy
import pickle
import xml.etree.ElementTree as ET

import pytest
from statemachine.contrib.diagram import DotGraphMachine
from statemachine.contrib.diagram import MermaidGraphMachine
from statemachine.exceptions import InvalidDefinition
from statemachine.io.scxml.parser import parse_literal_data
from statemachine.io.scxml.parser import parse_scxml
from statemachine.io.scxml.processor import SCXMLProcessor

from statemachine import DataChangeInfo
from statemachine import DataVar
from statemachine import HistoryState
from statemachine import State
from statemachine import StateChart


def _marker():
    return "called"


class TestDeclarations:
    def test_public_imports(self):
        assert DataVar is not None
        assert DataChangeInfo.__dataclass_fields__["state_id"]

    def test_invalid_declarations(self):
        with pytest.raises(InvalidDefinition, match="dict"):
            State(data=["n"])  # type: ignore[arg-type]
        with pytest.raises(InvalidDefinition, match="strings"):
            State(data={1: 0})  # type: ignore[dict-item]
        with pytest.raises(InvalidDefinition, match="both"):
            DataVar(default=1, factory=int)
        with pytest.raises(InvalidDefinition, match="type"):
            State(data={"n": DataVar(default="x", type=int)})

    def test_factory_type_mismatch_on_entry(self):
        class BadFactory(StateChart):
            a = State(
                initial=True,
                final=True,
                data={"n": DataVar(factory=lambda: "x", type=int)},
            )

        with pytest.raises(InvalidDefinition, match="type"):
            BadFactory()

    def test_undeclared_key_in_callback(self):
        class BadKey(StateChart):
            a = State(initial=True, final=True, data={"n": 0})

            def on_enter_a(self, state_data):
                state_data["nope"] = 1

        with pytest.raises(InvalidDefinition, match="nope"):
            BadKey()

    def test_data_var_without_default_produces_none(self):
        class Empty(StateChart):
            a = State(initial=True, final=True, data={"n": DataVar()})

        assert Empty().get_state_data("a")["n"] is None

    def test_callable_default_is_not_invoked(self):
        class HoldsCallable(StateChart):
            a = State(initial=True, final=True, data={"fn": DataVar(default=_marker)})

        assert HoldsCallable().get_state_data("a")["fn"] is _marker


class TestLifecycle:
    async def test_entry_exit_reset_and_isolation(self, sm_runner):
        seen = []

        def factory():
            seen.append(1)
            return len(seen)

        class Bucket(StateChart):
            a = State(initial=True, data={"items": list, "n": factory, "label": "box"})
            b = State()
            done = State(final=True)
            go = a.to(b)
            back = b.to(a)
            finish = a.to(done)

            def on_enter_a(self, state_data, source, target, event_data):
                self.enter_n = state_data["n"]
                self.saw_target = target.id == "a"
                self.saw_event = event_data is not None

            def on_exit_a(self, state_data, source):
                self.exit_n = state_data["n"]
                self.exit_source = source.id

        sm = await sm_runner.start(Bucket)
        assert sm.enter_n == 1
        assert sm.saw_target
        assert sm.saw_event
        first_items = sm.get_state_data(sm.a)["items"]
        first_items.append("kept")
        assert sm.get_state_data("a")["label"] == "box"
        assert sm.state_data_values["a"]["items"] == ["kept"]

        other = Bucket() if not sm_runner.is_async else await sm_runner.start(Bucket)
        assert other.get_state_data("a")["items"] == []
        assert sm.get_state_data("a")["items"] == ["kept"]
        assert "items" not in Bucket.a.__dict__

        snap = sm.state_data_values
        snap["a"]["items"].append("snap-only")
        assert sm.get_state_data("a")["items"] == ["kept"]

        await sm_runner.send(sm, "go")
        assert sm.exit_n == 1
        assert sm.exit_source == "a"
        assert sm.get_state_data("a") is None
        assert sm.get_state_data("missing") is None

        await sm_runner.send(sm, "back")
        second_items = sm.get_state_data("a")["items"]
        assert second_items == []
        assert second_items is not first_items
        assert sm.get_state_data("a")["n"] == len(seen)

    async def test_assignments_types_and_macrostep(self, sm_runner):
        class Steps(StateChart):
            a = State(
                initial=True,
                data={"n": DataVar(default=0, type=int), "label": DataVar(default="a", type=str)},
            )
            b = State(data={"n": 0})
            c = State(final=True)
            go = a.to(b)
            b.to(c)

            def on_enter_b(self, state_data):
                state_data["n"] = 7

        sm = await sm_runner.start(Steps)
        sm.set_state_data(sm.a, "n", 0)
        assert sm.get_data_changes() == []

        sm.set_state_data("a", "n", 3)
        change = sm.get_data_changes()[-1]
        assert change == DataChangeInfo(state_id="a", key="n", old_value=0, new_value=3)
        copied = sm.get_data_changes()
        copied.clear()
        assert sm.get_data_changes()[-1].new_value == 3

        live = sm.get_state_data("a")
        del live["n"]
        sm.set_state_data("a", "n", 4)
        assert sm.get_data_changes()[-1].old_value is None
        assert sm.get_data_changes()[-1].new_value == 4

        with pytest.raises(InvalidDefinition, match="label"):
            sm.set_state_data("a", "label", 1)
        with pytest.raises(InvalidDefinition, match="not declared"):
            sm.set_state_data("a", "missing", 1)
        with pytest.raises(InvalidDefinition, match="not declared"):
            live["missing"] = 1

        view = sm._scoped_state_data(sm.a)
        assert view == {"n": 4, "label": "a"}
        assert view.copy()["label"] == "a"
        assert "label" in repr(view)
        assert (view == 1) is False
        with pytest.raises(KeyError):
            view["missing"]
        with pytest.raises(InvalidDefinition):
            view["missing"] = 1
        with pytest.raises(KeyError):
            del view["missing"]
        del view["label"]
        assert "label" not in sm.get_state_data("a")

        await sm_runner.send(sm, "go")
        changes = sm.get_data_changes()
        assert [item.state_id for item in changes] == ["b"]
        assert changes[0].old_value == 0
        assert changes[0].new_value == 7
        with pytest.raises(InvalidDefinition, match="inactive"):
            sm.set_state_data("a", "n", 1)
        with pytest.raises(InvalidDefinition, match="inactive"):
            sm.set_state_data("nope", "n", 1)

        sm._activate_state_data(None, {})
        sm._deactivate_state_data(None)

    async def test_hierarchy_and_parallel_regions(self, sm_runner):
        class Nest(StateChart):
            class group(State.Compound, data={"level": "parent", "shared": 1}):
                child = State(initial=True, data={"name": "child", "level": "child"})

            done = State(final=True)
            leave = group.to(done)

            def on_enter_child(self, state_data):
                state_data["shared"] = 2
                self.seen = dict(state_data)

        sm = await sm_runner.start(Nest)
        assert sm.seen == {"level": "child", "name": "child", "shared": 2}
        assert sm.get_state_data(sm.group)["shared"] == 2
        assert sm.get_state_data("group")["level"] == "parent"
        assert sm.get_state_data("child")["level"] == "child"

        class Regions(StateChart):
            validate_trap_states = False
            validate_final_reachability = False

            class par(State.Parallel, data={"shared": "root"}):
                class left(State.Compound, data={"n": 1, "side": "L"}):
                    a = State(initial=True, data={"x": 10, "n": 100})

                class right(State.Compound, data={"n": 2, "side": "R"}):
                    b = State(initial=True, data={"y": 20})

            def on_enter_a(self, state_data):
                state_data["shared"] = "from-a"
                self.seen_a = dict(state_data)

            def on_enter_b(self, state_data):
                self.seen_b = dict(state_data)

        regions = await sm_runner.start(Regions)
        assert regions.seen_a == {"shared": "from-a", "n": 100, "side": "L", "x": 10}
        assert regions.seen_b["side"] == "R"
        assert regions.seen_b["n"] == 2
        assert regions.seen_b["y"] == 20
        assert regions.seen_b["shared"] == "from-a"
        assert "x" not in regions.seen_b
        assert "y" not in regions.seen_a

    def test_pickle_and_deepcopy(self):
        sm = PickleCounter()
        sm.set_state_data("idle", "n", 4)
        sm.get_state_data("idle")["items"].append(1)

        restored = pickle.loads(pickle.dumps(sm))
        assert restored.get_state_data("idle")["n"] == 4
        assert restored.get_state_data("idle")["items"] == [1]
        restored.set_state_data(restored.idle, "n", 5)
        assert restored.get_data_changes()[-1].new_value == 5
        with pytest.raises(InvalidDefinition):
            restored.set_state_data("idle", "n", "bad")

        cloned = copy.deepcopy(sm)
        assert cloned.get_state_data("idle")["n"] == 4
        assert cloned.get_state_data("idle")["items"] == [1]
        cloned.get_state_data("idle")["items"].append(2)
        assert sm.get_state_data("idle")["items"] == [1]

        plain = copy.deepcopy(sm.get_state_data("idle"))
        assert type(plain) is dict
        assert pickle.loads(pickle.dumps(sm.get_state_data("idle")))["n"] == 4

        sm._state_data["ghost"] = {"n": 1}
        del sm._data_changes
        del sm._history_data
        sm._rebind_state_data()
        assert "ghost" not in sm._state_data
        assert sm._data_changes == []
        assert sm._history_data == {}
        assert sm.get_state_data("idle")["n"] == 4


class PickleCounter(StateChart):
    idle = State(initial=True, data={"n": DataVar(default=0, type=int), "items": list})
    done = State(final=True)
    finish = idle.to(done)


class ZoneMachine(StateChart):
    class zone(State.Compound, data={"zone_n": 0}):
        hall = State(initial=True)

        class room(State.Compound, data={"room_n": DataVar(default=0, type=int)}):
            door = State(initial=True, data={"spot": "door"})
            desk = State(data={"spot": "desk"})
            move = door.to(desk)
            back = desk.to(door)

            def on_exit_desk(self, state_data, source):
                self.exit_spot = state_data["spot"]
                self.exit_source = source.id
                state_data["spot"] = "saved"

        assert isinstance(room, State)
        enter_room = hall.to(room)
        h = HistoryState()
        h_deep = HistoryState(type="deep")

    out = State(initial=True, data={"tag": "out"})
    go = out.to(zone)
    leave = zone.to(out)
    back_shallow = out.to(zone.h)  # type: ignore[has-type]
    back_deep = out.to(zone.h_deep)  # type: ignore[has-type]


class TestHistoryData:
    async def test_shallow_restores_direct_child_only(self, sm_runner):
        sm = await sm_runner.start(ZoneMachine)
        await sm_runner.send(sm, "go")
        assert len(sm._scoped_state_data(sm.zone.hall)) == 1
        assert sm._scoped_state_data(sm.zone.hall)["zone_n"] == 0
        await sm_runner.send(sm, "leave")
        assert sm._history_data[ZoneMachine.zone.h.id] == {}

        sm._history_data.clear()
        await sm_runner.send(sm, "back_shallow")
        assert "hall" in sm.configuration_values
        assert sm.get_state_data("zone")["zone_n"] == 0

        await sm_runner.send(sm, "leave")
        await sm_runner.send(sm, "go")
        await sm_runner.send(sm, "enter_room")
        await sm_runner.send(sm, "move")
        sm.set_state_data(sm.zone, "zone_n", 9)
        sm.set_state_data(sm.zone.room, "room_n", 4)
        sm.set_state_data(sm.zone.room.desk, "spot", "temp")
        await sm_runner.send(sm, "leave")
        assert sm.exit_spot == "temp"
        assert sm.exit_source == "zone"
        assert sm.get_state_data("desk") is None

        await sm_runner.send(sm, "back_shallow")
        assert sm.get_state_data("room")["room_n"] == 4
        assert sm.get_state_data("door")["spot"] == "door"
        assert sm.get_state_data("desk") is None
        assert sm.get_state_data("zone")["zone_n"] == 0

    async def test_deep_restores_descendants_and_exit_edits(self, sm_runner):
        sm = await sm_runner.start(ZoneMachine)
        await sm_runner.send(sm, "go")
        await sm_runner.send(sm, "enter_room")
        await sm_runner.send(sm, "move")
        sm.set_state_data("room", "room_n", 4)
        sm.set_state_data("desk", "spot", "temp")
        await sm_runner.send(sm, "leave")
        await sm_runner.send(sm, "back_deep")
        assert sm.get_state_data("desk")["spot"] == "saved"
        assert sm.get_state_data("room")["room_n"] == 4
        assert sm.get_state_data("door") is None
        assert "desk" in sm.configuration_values

    async def test_corrupt_snapshot_is_rejected(self, sm_runner):
        sm = await sm_runner.start(ZoneMachine)
        await sm_runner.send(sm, "go")
        await sm_runner.send(sm, "enter_room")
        await sm_runner.send(sm, "leave")
        sm._history_data[ZoneMachine.zone.h_deep.id]["room"]["room_n"] = "nope"
        with pytest.raises(InvalidDefinition, match="room_n"):
            await sm_runner.send(sm, "back_deep")

    async def test_missing_snapshot_key_uses_default(self, sm_runner):
        sm = await sm_runner.start(ZoneMachine)
        await sm_runner.send(sm, "go")
        await sm_runner.send(sm, "enter_room")
        sm.set_state_data("room", "room_n", 4)
        await sm_runner.send(sm, "leave")
        del sm._history_data[ZoneMachine.zone.h_deep.id]["room"]["room_n"]
        await sm_runner.send(sm, "back_deep")
        assert sm.get_state_data("room")["room_n"] == 0


class TestScxmlData:
    def test_literals_are_state_data_and_non_literals_are_skipped(self):
        scxml = """
        <scxml initial="s">
          <datamodel>
            <data id="root_n" expr="2"/>
            <data id="n" expr="1"/>
            <data id="bad" expr="1 + 2"/>
          </datamodel>
          <state id="s">
            <datamodel>
              <data id="n" expr="9"/>
              <data id="items" expr="[1, 2]"/>
            </datamodel>
            <transition event="go" target="done"/>
          </state>
          <final id="done"/>
        </scxml>
        """
        definition = parse_scxml(scxml)
        assert definition.root_data == {"root_n": 2, "n": 1}
        assert definition.states["s"].data == {"n": 9, "items": [1, 2]}

        processor = SCXMLProcessor()
        processor.parse_scxml("literals", scxml)
        sm = processor.start()
        assert sm.get_state_data("s")["n"] == 9
        assert sm.get_state_data("s")["root_n"] == 2
        assert sm.get_state_data("s")["items"] == [1, 2]
        assert "bad" not in sm.get_state_data("s")

        root_only = """
        <scxml initial="s">
          <datamodel>
            <data id="root_n" expr="2"/>
          </datamodel>
          <state id="s">
            <transition event="go" target="done"/>
          </state>
          <final id="done"/>
        </scxml>
        """
        root_processor = SCXMLProcessor()
        root_processor.parse_scxml("root-only", root_only)
        assert root_processor.start().get_state_data("s")["root_n"] == 2

        fragment = ET.fromstring(
            """
            <state>
              <datamodel>
                <data id="ok" expr="3"/>
                <data id="bad" expr="1 + 2"/>
                <data expr="4"/>
                <data id="bare"/>
              </datamodel>
              <state id="nested">
                <datamodel><data id="inner" expr="5"/></datamodel>
              </state>
            </state>
            """
        )
        assert parse_literal_data(fragment) == {"ok": 3}

    def test_nested_datamodel_stays_on_its_state(self):
        scxml = """
        <scxml initial="parent">
          <state id="parent" initial="child">
            <datamodel>
              <data id="only_parent" expr="1"/>
            </datamodel>
            <state id="child">
              <datamodel>
                <data id="only_child" expr="'c'"/>
              </datamodel>
            </state>
            <transition event="go" target="done"/>
          </state>
          <final id="done"/>
        </scxml>
        """
        processor = SCXMLProcessor()
        processor.parse_scxml("nested", scxml)
        sm = processor.start()
        assert dict(sm.get_state_data("parent")) == {"only_parent": 1}
        assert sm.get_state_data("child")["only_child"] == "c"
        assert sm._scoped_state_data(sm.child)["only_parent"] == 1


class TestDiagrams:
    def test_dot_and_mermaid_annotate_data(self):
        class Drawn(StateChart):
            validate_trap_states = False
            validate_final_reachability = False
            validate_disconnected_states = False

            class group(State.Compound, data={"level": "parent"}):
                child = State(initial=True, data={"name": "child"}, enter="on_enter_child")

                def on_enter_child(self):
                    return None

            class par(State.Parallel, data={"shared": 1}):
                class left(State.Compound):
                    a = State(initial=True)

                class right(State.Compound):
                    b = State(initial=True)

            done = State(final=True)
            leave = group.to(done)

        dot = DotGraphMachine(Drawn).get_graph().to_string()
        assert "data: name" in dot
        assert "data: level" in dot
        assert "data: shared" in dot

        mermaid = MermaidGraphMachine(Drawn).get_mermaid()
        assert "data: name" in mermaid
        assert "data: level" in mermaid
        assert "data: shared" in mermaid
