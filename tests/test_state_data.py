import pickle

import pytest
from statemachine.exceptions import InvalidDefinition
from statemachine.io.scxml.parser import _literal_data_map
from statemachine.io.scxml.parser import parse_scxml
from statemachine.io.scxml.processor import SCXMLProcessor

from statemachine import DataChangeInfo
from statemachine import DataVar
from statemachine import HistoryState
from statemachine import State
from statemachine import StateChart


def test_invalid_data_declarations():
    with pytest.raises(InvalidDefinition):
        State(data=["nope"])  # type: ignore[arg-type]
    with pytest.raises(InvalidDefinition):
        State(data={1: "x"})  # type: ignore[dict-item]
    with pytest.raises(InvalidDefinition):
        DataVar(default=1, factory=list)


class _Owner(StateChart):
    s1 = State(
        initial=True,
        data={"n": 0, "items": list, "flag": DataVar(default=True, type=bool)},
    )
    s2 = State(data={"n": DataVar(factory=lambda: 1, type=int)})
    go = s1.to(s2)
    back = s2.to(s1)

    def __init__(self):
        self.seen = {}
        super().__init__()

    def on_enter_s1(self, state_data):
        self.seen["enter"] = dict(state_data)

    def on_exit_s1(self, state_data):
        self.seen["exit"] = dict(state_data)


def test_entry_exit_reset_and_instance_isolation():
    sm = _Owner()
    assert sm.seen["enter"]["n"] == 0
    assert sm.seen["enter"]["flag"] is True
    first_items = sm.get_state_data("s1")["items"]
    assert first_items == []
    assert sm.get_state_data(sm.s2) is None

    sm.set_state_data(sm.s1, "n", 5)
    sm.set_state_data("s1", "items", [1])
    assert sm.get_state_data(sm.s1)["n"] == 5

    other = _Owner()
    assert other.get_state_data("s1")["n"] == 0
    assert other.get_state_data("s1")["items"] is not first_items

    sm.send("go")
    assert sm.seen["exit"]["n"] == 5
    assert sm.get_state_data("s1") is None
    assert sm.get_state_data("s2")["n"] == 1

    sm.send("back")
    assert sm.get_state_data("s1")["n"] == 0
    assert sm.get_state_data("s1")["items"] == []
    assert sm.get_state_data("s1")["items"] is not first_items


def test_set_state_data_validation_and_changes():
    sm = _Owner()
    init_changes = sm.get_data_changes()
    assert any(
        isinstance(c, DataChangeInfo) and c.key == "n" and c.new_value == 0 for c in init_changes
    )

    sm.set_state_data("s1", "n", 3)
    change = sm.get_data_changes()[-1]
    assert change.state_id == "s1"
    assert change.old_value == 0
    assert change.new_value == 3

    with pytest.raises(InvalidDefinition):
        sm.set_state_data("s1", "missing", 1)
    with pytest.raises(InvalidDefinition):
        sm.set_state_data("s1", "flag", "no")
    with pytest.raises(InvalidDefinition):
        sm.set_state_data("s2", "n", 1)

    raw = next(s for s in sm.states_map.values() if s.id == "s1")
    assert sm.get_state_data(raw)["n"] == 3
    assert sm.scoped_state_data(None) == {}

    snapshot = sm.state_data_values
    snapshot["s1"]["n"] = 99
    assert sm.get_state_data("s1")["n"] == 3

    sm.send("go")
    assert sm.get_data_changes() == [
        DataChangeInfo(state_id="s2", key="n", old_value=None, new_value=1)
    ]
    with pytest.raises(InvalidDefinition):
        sm.set_state_data("unknown", "n", 1)


def test_factory_type_enforced_on_entry():
    class Bad(StateChart):
        s = State(initial=True, final=True, data={"n": DataVar(factory=lambda: "x", type=int)})

    with pytest.raises(InvalidDefinition):
        Bad()


def test_untyped_datavar_and_plain_default_copy():
    produced = DataVar(factory=lambda: {"k": 1}).produce()
    produced["k"] = 2
    assert DataVar(factory=lambda: {"k": 1}).produce() == {"k": 1}
    DataVar(type=int).check(None)
    DataVar().produce()

    class M(StateChart):
        s = State(initial=True, final=True, data={"box": {"k": 1}})

    sm = M()
    sm.get_state_data("s")["box"]["k"] = 5
    sm2 = M()
    assert sm2.get_state_data("s")["box"]["k"] == 1


class _Scoped(StateChart):
    class parent(State.Compound, data={"shared": "parent", "own": 1}):
        child = State(initial=True, data={"shared": "child", "leaf": 2})

    def on_enter_child(self, state_data):
        self.scope = dict(state_data)


def test_hierarchical_scope_shadows_parent():
    sm = _Scoped()
    assert sm.scope == {"shared": "child", "own": 1, "leaf": 2}
    assert sm.get_state_data("parent")["shared"] == "parent"


class _Parallel(StateChart):
    class par(State.Parallel, data={"shared": "p"}):
        class r1(State.Compound, data={"region": "1", "shared": "r1"}):
            a = State(initial=True, data={"leaf": "a"})

        class r2(State.Compound, data={"region": "2", "shared": "r2"}):
            b = State(initial=True, data={"leaf": "b"})

    def __init__(self):
        self.scopes = {}
        super().__init__()

    def on_enter_a(self, state_data):
        self.scopes["a"] = dict(state_data)

    def on_enter_b(self, state_data):
        self.scopes["b"] = dict(state_data)


def test_parallel_regions_isolate_scopes():
    sm = _Parallel()
    assert sm.scopes["a"]["shared"] == "r1"
    assert sm.scopes["a"]["region"] == "1"
    assert "leaf" in sm.scopes["a"]
    assert "region" not in sm.scopes["a"] or sm.scopes["a"]["region"] == "1"
    assert sm.scopes["b"]["shared"] == "r2"
    assert sm.scopes["a"].keys().isdisjoint({"leaf"}) or sm.scopes["b"]["leaf"] == "b"
    assert "a" not in str(sm.scopes["b"].get("leaf"))


class _Shallow(StateChart):
    class realm(State.Compound):
        class inner(State.Compound, data={"level": 0}):
            entrance = State(initial=True, data={"n": 0})
            chamber = State(data={"n": 0})
            explore = entrance.to(chamber)

        h = HistoryState()

    outside = State(initial=False)
    escape = realm.to(outside)
    back = outside.to(realm.h)


class _Deep(StateChart):
    class realm(State.Compound):
        class inner(State.Compound, data={"level": 0}):
            entrance = State(initial=True, data={"n": 0})
            chamber = State(data={"n": 0})
            explore = entrance.to(chamber)

        h = HistoryState(type="deep")

    outside = State()
    escape = realm.to(outside)
    back = outside.to(realm.h)


def test_history_restores_shallow_and_deep_snapshots():
    shallow = _Shallow()
    shallow.set_state_data("inner", "level", 7)
    shallow.set_state_data("entrance", "n", 4)
    shallow.send("explore")
    shallow.set_state_data("chamber", "n", 9)
    shallow.send("escape")
    shallow.send("back")
    assert shallow.get_state_data("inner")["level"] == 7
    assert "entrance" in shallow.configuration_values
    assert shallow.get_state_data("entrance")["n"] == 0

    deep = _Deep()
    deep.set_state_data("inner", "level", 7)
    deep.send("explore")
    deep.set_state_data("chamber", "n", 9)
    deep.send("escape")
    deep.send("back")
    assert deep.get_state_data("inner")["level"] == 7
    assert deep.get_state_data("chamber")["n"] == 9


def test_data_survives_pickle():
    sm = _Owner()
    sm.set_state_data("s1", "n", 8)
    restored = pickle.loads(pickle.dumps(sm))
    assert restored.get_state_data("s1")["n"] == 8


def test_scxml_datamodel_literals():
    xml = """
    <scxml initial="s1">
      <datamodel>
        <data id="root_n" expr="4"/>
        <data id="expr" expr="1+2"/>
      </datamodel>
      <state id="s1">
        <datamodel>
          <data id="child_n" expr="5"/>
          <data id="label" expr="'hi'"/>
          <data id="empty"/>
        </datamodel>
      </state>
    </scxml>
    """
    definition = parse_scxml(xml)
    assert definition.root_data["root_n"] == 4
    assert "expr" not in definition.root_data
    assert definition.states["s1"].data["child_n"] == 5
    assert definition.states["s1"].data["label"] == "hi"
    assert definition.states["s1"].data["empty"] is None

    processor = SCXMLProcessor()
    processor.parse_scxml("withdata", xml)
    sm = processor.start()
    assert sm.get_state_data("s1")["root_n"] == 4
    assert sm.get_state_data("s1")["child_n"] == 5

    import xml.etree.ElementTree as ET

    skipped = _literal_data_map(ET.fromstring("<datamodel><data>1</data></datamodel>"))
    assert skipped == {}
    from_text = _literal_data_map(
        ET.fromstring('<datamodel><data id="n">3</data></datamodel>')
    )
    assert from_text["n"] == 3


def test_diagram_annotates_data_vars():
    from statemachine.contrib.diagram import DotGraphMachine
    from statemachine.contrib.diagram.extract import extract

    class M(StateChart):
        s1 = State(initial=True, data={"n": 0, "items": list})
        s2 = State(final=True)
        go = s1.to(s2)

    graph = extract(M)
    assert graph.states[0].data_vars == ["n", "items"]
    dot = DotGraphMachine(M)().to_string()
    assert "data / n, items" in dot

    from statemachine.contrib.diagram.model import DiagramState
    from statemachine.contrib.diagram.model import StateType
    from statemachine.contrib.diagram.renderers.mermaid import MermaidRenderer

    rendered = MermaidRenderer().render(extract(_Parallel))
    assert "data /" in rendered
    lines: list[str] = []
    MermaidRenderer._append_data_comment(
        DiagramState(id="x", name="x", type=StateType.REGULAR), lines, 1
    )
    assert lines == []
