import shutil

import pytest

from notifications_server.utils.mermaid_graph import (
    _MAX_NODES,
    _build_graphviz,
    _parse_flowchart,
    render_flowchart_image,
)

DOT_MISSING = shutil.which("dot") is None


class TestParseFlowchart:
    def test_simple_edge_parses(self):
        parsed = _parse_flowchart('graph TD\n    S1["API Gateway"] --> S2["Auth Service"]\n')
        assert parsed is not None
        rankdir, root, labels, shapes, edges = parsed
        assert rankdir == "TB"
        assert labels == {"S1": "API Gateway", "S2": "Auth Service"}
        assert [(*e.__dict__.values(),) for e in edges] == [("S1", "S2", None, "-->")]

    def test_kebab_case_node_ids_parse(self):
        # Real-world (especially Kubernetes-sourced) diagrams routinely use
        # kebab-case ids like `cert-manager` - verified against a live
        # diagram whose entire node list used this style and failed to
        # parse when the id pattern was plain `\w+`.
        parsed = _parse_flowchart('graph LR\n    cert-manager["cert-manager"] --> demo-vanshika["demo-vanshika"]\n')
        assert parsed is not None
        _, _, labels, _, edges = parsed
        assert labels == {"cert-manager": "cert-manager", "demo-vanshika": "demo-vanshika"}
        assert [(e.source, e.target) for e in edges] == [("cert-manager", "demo-vanshika")]

    def test_kebab_case_subgraph_ids_parse(self):
        # _SINGLE_WORD_RE (subgraph ids) needs the same kebab-case tolerance
        # as the node-id pattern above - otherwise a kebab-case subgraph id
        # like `cert-manager` isn't recognized as a valid subgraph id, and an
        # edge referencing it registers a phantom node instead of pointing
        # at the cluster.
        code = (
            "graph TD\n"
            "    Cluster --> cert-manager\n"
            '    subgraph cert-manager ["Cert Manager"]\n'
            '        A["a"]\n'
            "    end\n"
        )
        parsed = _parse_flowchart(code)
        assert parsed is not None
        _, root, labels, _, _ = parsed
        assert "cert-manager" not in labels
        assert root.children[0].subgraph_id == "cert-manager"

    def test_kebab_case_node_id_does_not_swallow_an_unspaced_arrow(self):
        # A hyphen must be immediately followed by another word character to
        # join the id - otherwise a bare/kebab-case id right up against an
        # unspaced arrow (`A-->B`, no surrounding whitespace) would greedily
        # eat the arrow's leading dash(es) and break the parse.
        for arrow in ("-->", "---", "-.->"):
            parsed = _parse_flowchart(f"graph LR\n    cert-manager{arrow}B\n")
            assert parsed is not None, arrow
            _, _, _, _, edges = parsed
            assert [(e.source, e.target, e.arrow) for e in edges] == [("cert-manager", "B", arrow)]

    def test_lr_direction_maps_through(self):
        parsed = _parse_flowchart('graph LR\n    A["a"] --> B["b"]\n')
        assert parsed[0] == "LR"

    def test_subgraph_groups_its_nodes(self):
        code = (
            "graph TD\n"
            '    subgraph "Service Mesh"\n'
            '        S1["API Gateway"] --> S2["Auth Service"]\n'
            '        S2 --> DB1[("User DB")]\n'
            "    end\n"
        )
        parsed = _parse_flowchart(code)
        assert parsed is not None
        _, root, labels, shapes, edges = parsed
        assert len(root.children) == 1
        cluster = root.children[0]
        assert cluster.title == "Service Mesh"
        assert cluster.node_ids == ["S1", "S2", "DB1"]
        assert labels["DB1"] == "User DB"
        assert shapes["DB1"] == "[("

    def test_nested_subgraphs(self):
        code = (
            "graph TD\n"
            '    subgraph "Outer"\n'
            '        A["a"] --> B["b"]\n'
            '        subgraph "Inner"\n'
            '            C["c"] --> D["d"]\n'
            "        end\n"
            "    end\n"
        )
        parsed = _parse_flowchart(code)
        assert parsed is not None
        _, root, *_ = parsed
        outer = root.children[0]
        assert outer.title == "Outer"
        assert outer.node_ids == ["A", "B"]
        inner = outer.children[0]
        assert inner.title == "Inner"
        assert inner.node_ids == ["C", "D"]

    def test_direction_inside_subgraph_is_captured(self):
        code = (
            "graph TD\n" '    subgraph "Pipeline"\n' "        direction LR\n" '        A["a"] --> B["b"]\n' "    end\n"
        )
        parsed = _parse_flowchart(code)
        assert parsed is not None
        _, root, *_ = parsed
        assert root.children[0].direction == "LR"
        assert root.children[0].node_ids == ["A", "B"]

    def test_direction_td_normalizes_to_tb(self):
        code = 'graph TD\n    subgraph "S"\n        direction TD\n        A["a"]\n    end\n'
        parsed = _parse_flowchart(code)
        assert parsed[1].children[0].direction == "TB"

    def test_subgraph_without_direction_leaves_it_unset(self):
        code = 'graph TD\n    subgraph "S"\n        A["a"] --> B["b"]\n    end\n'
        parsed = _parse_flowchart(code)
        assert parsed[1].children[0].direction is None

    def test_subgraph_id_form_captures_the_id(self):
        code = 'graph TD\n    subgraph NS ["Namespace: ingress-nginx"]\n        A["a"]\n    end\n'
        parsed = _parse_flowchart(code)
        assert parsed[1].children[0].subgraph_id == "NS"
        assert parsed[1].children[0].title == "Namespace: ingress-nginx"

    def test_subgraph_bracket_title_may_be_unquoted(self):
        # Real Mermaid accepts an unquoted title inside the brackets too,
        # not just a quoted one - verified against a live diagram
        # (`subgraph NS [nudgebee-agent Namespace]`).
        code = "graph TD\n    subgraph NS [nudgebee-agent Namespace]\n        A[a]\n    end\n"
        parsed = _parse_flowchart(code)
        assert parsed is not None
        assert parsed[1].children[0].subgraph_id == "NS"
        assert parsed[1].children[0].title == "nudgebee-agent Namespace"

    def test_subgraph_multiword_id_before_bracket_has_no_edge_referenceable_id(self):
        # Real Mermaid also tolerates a multi-word, unquoted id before the
        # bracket - verified against a live diagram
        # (`subgraph K8s Cluster [nudgebee-agent Namespace]`), which used to
        # fail the whole parse since neither the id-bracket alternative
        # (single \w+ id only) nor the bracket-free unquoted-title
        # alternative (no bracket chars allowed) matched it. The bracketed
        # title still wins for display; the multi-word id/text itself can't
        # be an edge's target token (same rule as a multi-word bracket-free
        # title), so subgraph_id is None here.
        code = "graph LR\n    subgraph K8s Cluster [nudgebee-agent Namespace]\n        A[a]\n    end\n"
        parsed = _parse_flowchart(code)
        assert parsed is not None
        assert parsed[1].children[0].subgraph_id is None
        assert parsed[1].children[0].title == "nudgebee-agent Namespace"

    def test_single_word_unquoted_subgraph_title_also_acts_as_its_id(self):
        # Real Mermaid's simplest subgraph form is just `subgraph NS` (no
        # title in brackets at all) - NS doubles as both the id and the
        # displayed title. An edge can then reference it by that same bare
        # word, same as the explicit `subgraph ID ["Title"]` form.
        code = 'graph TD\n    Cluster["c"]\n    subgraph NS\n        A["a"]\n    end\n    Cluster --> NS\n'
        parsed = _parse_flowchart(code)
        assert parsed is not None
        _, root, labels, _, edges = parsed
        assert root.children[0].subgraph_id == "NS"
        assert root.children[0].title == "NS"
        assert "NS" not in labels  # never registered as a phantom node
        assert root.node_ids == ["Cluster"]
        assert [(e.source, e.target) for e in edges] == [("Cluster", "NS")]

    def test_unquoted_subgraph_title_is_parsed(self):
        # Real Mermaid.js accepts an unquoted subgraph title fine even with
        # spaces and "&" - verified against a live diagram the frontend
        # rendered correctly (`subgraph Frontend & API Layer`) while our
        # parser rejected it outright. Same quoted-or-unquoted tolerance
        # already given to node labels.
        code = "graph LR\n    subgraph Frontend & API Layer\n        UI[app-dev UI]\n    end\n"
        parsed = _parse_flowchart(code)
        assert parsed is not None
        assert parsed[1].children[0].title == "Frontend & API Layer"
        assert parsed[1].children[0].subgraph_id is None  # no bracket, nothing to hold an id
        assert parsed[1].children[0].node_ids == ["UI"]

    def test_unquoted_subgraph_title_has_no_id_so_cannot_be_edge_target(self):
        # Without a bracketed id there's nothing an edge could reference -
        # "Some" below is just an ordinary new node, unrelated to the
        # "Some Title" subgraph, not a reference to it (only the `ID
        # ["Title"]` form supports edge-to-subgraph - see
        # test_edge_naming_a_subgraph_is_not_registered_as_a_phantom_node).
        code = 'graph LR\n    subgraph Some Title\n        A["a"]\n    end\n    X["x"] --> Some\n'
        parsed = _parse_flowchart(code)
        assert parsed is not None
        _, root, labels, _, edges = parsed
        assert "Some" not in labels
        assert "Some" in root.node_ids  # an ordinary, unrelated bare node
        assert [(e.source, e.target) for e in edges] == [("X", "Some")]

    def test_quoted_title_subgraph_has_no_id(self):
        code = 'graph TD\n    subgraph "S"\n        A["a"]\n    end\n'
        parsed = _parse_flowchart(code)
        assert parsed[1].children[0].subgraph_id is None

    def test_edge_naming_a_subgraph_is_not_registered_as_a_phantom_node(self):
        # Real Mermaid feature: an edge may name a subgraph directly to
        # point at the whole cluster (`Cluster --> NS`), not a node inside
        # it. Regression: this used to create a brand-new, empty, unlabeled
        # node called "NS", disconnected from the real "Namespace:
        # ingress-nginx" cluster - see project memory for the real diagram
        # this was diagnosed from.
        code = (
            "graph TD\n"
            '    Cluster["Kubernetes Cluster"]\n'
            '    subgraph NS ["Namespace: ingress-nginx"]\n'
            '        A["a"]\n'
            "    end\n"
            "    Cluster --> NS\n"
        )
        parsed = _parse_flowchart(code)
        assert parsed is not None
        _, root, labels, _, edges = parsed
        assert "NS" not in labels
        assert root.node_ids == ["Cluster"]  # NOT ["Cluster", "NS"]
        assert [(e.source, e.target) for e in edges] == [("Cluster", "NS")]

    def test_edge_can_name_a_subgraph_declared_later_in_the_file(self):
        # The subgraph-id pre-scan means this isn't order-dependent, unlike
        # a plain node reference.
        code = (
            "graph TD\n"
            '    Cluster["Kubernetes Cluster"]\n'
            "    Cluster --> NS\n"
            '    subgraph NS ["Namespace: ingress-nginx"]\n'
            '        A["a"]\n'
            "    end\n"
        )
        parsed = _parse_flowchart(code)
        assert parsed is not None
        assert "NS" not in parsed[2]  # labels

    def test_giving_a_subgraph_id_a_shape_is_ambiguous_and_fails_closed(self):
        # NS is already a subgraph's id; using it again as a labeled node
        # is a genuine ambiguity this simplified model can't safely
        # resolve - reject rather than guess (module docstring).
        code = (
            "graph TD\n"
            '    subgraph NS ["Namespace"]\n'
            '        A["a"]\n'
            "    end\n"
            '    NS["Also a node?"] --> A\n'
        )
        assert _parse_flowchart(code) is None

    def test_edge_label_is_captured(self):
        parsed = _parse_flowchart('graph LR\n    A["Start"] -->|"yes"| B["End"]\n')
        assert parsed is not None
        edges = parsed[4]
        assert edges[0].label == "yes"

    def test_bare_reference_to_already_labeled_node(self):
        code = 'graph TD\n    A["Start"] --> B["Mid"]\n    B --> C["End"]\n'
        parsed = _parse_flowchart(code)
        assert parsed is not None
        _, _, labels, _, edges = parsed
        assert labels["B"] == "Mid"
        assert [(e.source, e.target) for e in edges] == [("A", "B"), ("B", "C")]

    def test_arrow_without_surrounding_spaces_parses(self):
        assert _parse_flowchart('graph LR\n    A["x"]-->B["y"]\n') is not None

    def test_multiline_label_break_is_normalized(self):
        parsed = _parse_flowchart('graph TD\n    A["Line1<br/>Line2"] --> B["b"]\n')
        assert parsed[2]["A"] == "Line1\nLine2"

    def test_unquoted_node_label_is_parsed(self):
        # Real generated diagrams don't always quote a label, same tolerance
        # mermaid_chart.py's xychart/pie parsers already have (see module
        # docstring).
        parsed = _parse_flowchart("graph TD\n    A[Start] --> B[End]\n")
        assert parsed is not None
        assert parsed[2] == {"A": "Start", "B": "End"}

    def test_unquoted_cylinder_label_with_punctuation(self):
        # Exact real-world shape that motivated this: DB[(PostgreSQL
        # Database: nudgebee)] - unquoted, contains a colon and spaces.
        parsed = _parse_flowchart("graph TD\n    A[Start] --> DB[(PostgreSQL Database: nudgebee)]\n")
        assert parsed is not None
        assert parsed[2]["DB"] == "PostgreSQL Database: nudgebee"
        assert parsed[3]["DB"] == "[("

    def test_unquoted_label_may_contain_other_shapes_bracket_chars(self):
        # Real-world shape that motivated this: an unquoted rect label with
        # a literal paren (`Pod: Stateful Instance (Ordinal)`) used to fail
        # closed - real Mermaid.js (verified in the frontend) parses it
        # fine, since "(" ")" aren't part of a rect "[...]" shape's own
        # delimiter, so there's no real ambiguity. Each shape now gets its
        # own label character class (see _unquoted_label_class) rather than
        # one shared class banning every bracket type everywhere.
        parsed = _parse_flowchart("graph TD\n    P2[Pod: Stateful Instance (Ordinal)] --> B[b]\n")
        assert parsed is not None
        assert parsed[2]["P2"] == "Pod: Stateful Instance (Ordinal)"

    def test_unquoted_rhombus_label_may_contain_parens(self):
        parsed = _parse_flowchart("graph TD\n    A{Decision (yes/no)} --> B[b]\n")
        assert parsed is not None
        assert parsed[2]["A"] == "Decision (yes/no)"

    def test_unquoted_rounded_label_still_cannot_contain_its_own_parens(self):
        # Rounded's own delimiter IS "(" ")" - a literal ")" inside would be
        # indistinguishable from the real closer, so this must still fail
        # closed rather than being approximated.
        assert _parse_flowchart("graph TD\n    A(text (nested)) --> B[b]\n") is None

    def test_ambiguous_cylinder_syntax_falls_back_to_a_rect_interpretation(self):
        # "A[(text (nested))]" is genuinely ambiguous between a cylinder
        # labeled "text (nested)" and a rect labeled "(text (nested))" -
        # the cylinder branch can't match (its own label class excludes
        # parens, since "(" ")" are part of ITS delimiter), so the
        # alternation falls back to the rect interpretation instead. The
        # full text survives intact either way - a shape misread, not lost
        # or misrepresented content, which is the thing this module
        # actually guards against (see module docstring).
        parsed = _parse_flowchart("graph TD\n    A[(text (nested))] --> B[b]\n")
        assert parsed is not None
        assert parsed[2]["A"] == "(text (nested))"
        assert parsed[3]["A"] == "["

    def test_unquoted_label_needing_a_literal_closing_bracket_still_fails_closed(self):
        # "]" is always some shape's real closer (rect/subroutine/cylinder/
        # flag) - there's no shape whose label class can safely include it,
        # so this stays genuinely unparseable regardless of which shape is
        # tried.
        assert _parse_flowchart("graph TD\n    A[text ] bracket] --> B[b]\n") is None

    def test_unquoted_edge_label_is_parsed(self):
        parsed = _parse_flowchart('graph TD\n    A["Start"] -->|HTTPS| B["End"]\n')
        assert parsed is not None
        assert parsed[4][0].label == "HTTPS"

    def test_edge_label_with_slash_and_space(self):
        parsed = _parse_flowchart('graph TD\n    A["Start"] -->|WebSocket / HTTP| B["End"]\n')
        assert parsed is not None
        assert parsed[4][0].label == "WebSocket / HTTP"

    def test_bidirectional_arrow_is_parsed(self):
        parsed = _parse_flowchart('graph TD\n    A["Start"] <--> B["End"]\n')
        assert parsed is not None
        assert [(e.source, e.target) for e in parsed[4]] == [("A", "B")]
        assert parsed[4][0].arrow == "<-->"

    def test_bidirectional_arrow_with_unquoted_label(self):
        parsed = _parse_flowchart('graph TD\n    A["Start"] <-->|WebSocket / HTTP| B["End"]\n')
        assert parsed is not None
        assert parsed[4][0].label == "WebSocket / HTTP"
        assert parsed[4][0].arrow == "<-->"

    def test_undirected_arrow_is_parsed(self):
        parsed = _parse_flowchart('graph TD\n    A["a"] --- B["b"]\n')
        assert parsed is not None
        assert parsed[4][0].arrow == "---"

    def test_dotted_arrow_is_parsed(self):
        parsed = _parse_flowchart('graph TD\n    A["a"] -.-> B["b"]\n')
        assert parsed is not None
        assert parsed[4][0].arrow == "-.->"

    def test_compound_edge_left_side_expands_to_two_edges(self):
        # Mermaid's `A & B --> C` means both A-->C and B-->C, not a single
        # node literally named "A & B" - real-world shape that motivated
        # this (a K8s architecture diagram: `Pods & Nodes --> OTel`).
        code = 'graph TD\n    A["a"] --> B["b"]\n    C["c"] --> D["d"]\n    B & D --> E["e"]\n'
        parsed = _parse_flowchart(code)
        assert parsed is not None
        edges = [(e.source, e.target) for e in parsed[4]]
        assert ("B", "E") in edges
        assert ("D", "E") in edges

    def test_compound_edge_right_side_expands_to_two_edges(self):
        parsed = _parse_flowchart('graph TD\n    A["a"] --> B["b"] & C["c"]\n')
        assert parsed is not None
        edges = [(e.source, e.target) for e in parsed[4]]
        assert set(edges) == {("A", "B"), ("A", "C")}

    def test_compound_edge_both_sides_is_full_cross_product(self):
        parsed = _parse_flowchart('graph TD\n    A["a"] & B["b"] --> C["c"] & D["d"]\n')
        assert parsed is not None
        edges = {(e.source, e.target) for e in parsed[4]}
        assert edges == {("A", "C"), ("A", "D"), ("B", "C"), ("B", "D")}

    def test_compound_edge_label_applies_to_every_expanded_edge(self):
        parsed = _parse_flowchart(
            'graph TD\n    A["a"] --> B["b"]\n    C["c"] --> D["d"]\n    B & D -->|shared| E["e"]\n'
        )
        assert parsed is not None
        labels = {(e.source, e.target): e.label for e in parsed[4]}
        assert labels[("B", "E")] == "shared"
        assert labels[("D", "E")] == "shared"

    def test_compound_edge_with_bare_references(self):
        # Both sides reference already-declared nodes (no inline brackets) -
        # the shape used in the diagram that motivated this fix.
        code = 'graph TD\n    A["a"] --> B["b"]\n    C["c"] --> D["d"]\n    E["e"] --> F["f"]\n    B & D --> E\n'
        parsed = _parse_flowchart(code)
        assert parsed is not None
        edges = {(e.source, e.target) for e in parsed[4]}
        assert ("B", "E") in edges
        assert ("D", "E") in edges

    def test_quoted_label_containing_ampersand_is_unaffected(self):
        # Regression guard: a quoted label with a literal "&" must still
        # match the plain single-node _EDGE_RE (as it always did) rather
        # than being misrouted into the compound-edge path.
        parsed = _parse_flowchart('graph TD\n    A["Foo & Bar"] --> B["b"]\n')
        assert parsed is not None
        assert parsed[2]["A"] == "Foo & Bar"
        assert [(e.source, e.target) for e in parsed[4]] == [("A", "B")]

    def test_invalid_compound_piece_fails_closed(self):
        # One side has an unquoted label with a same-family bracket char
        # (a literal "[" inside a rect "[...]" label) - genuinely ambiguous
        # with the shape's own closer, so still invalid on its own even
        # after shape-aware unquoted labels (see
        # test_unquoted_label_may_contain_other_shapes_bracket_chars) -
        # the whole compound edge (and thus the diagram) fails closed
        # rather than silently dropping that piece.
        assert _parse_flowchart('graph TD\n    A[a [b]] & B["b"] --> C["c"]\n') is None

    def test_dash_text_edge_label_is_parsed(self):
        # Real Mermaid's other edge-label spelling (text between two dash
        # groups instead of after the arrow in pipes) - confirmed real via
        # llm-server's own "valid complex scenario" test fixture
        # (tool_mermaid_validation_test.go's TestComplexScenario uses this
        # form throughout).
        parsed = _parse_flowchart('graph TD\n    A["a"] -- Pulls Image From --> B["b"]\n')
        assert parsed is not None
        assert parsed[4][0].label == "Pulls Image From"
        assert parsed[4][0].arrow == "-->"

    def test_dash_text_edge_label_quoted(self):
        parsed = _parse_flowchart('graph TD\n    A["a"] -- "Some / Label" --> B["b"]\n')
        assert parsed is not None
        assert parsed[4][0].label == "Some / Label"

    def test_dash_text_edge_label_undirected(self):
        parsed = _parse_flowchart('graph TD\n    A["a"] -- Peers With --- B["b"]\n')
        assert parsed is not None
        assert parsed[4][0].label == "Peers With"
        assert parsed[4][0].arrow == "---"

    def test_plain_dash_edge_without_text_is_unaffected(self):
        # Regression guard: an ordinary unlabeled "---" must still match the
        # existing _EDGE_RE path, never the new dash-text handler.
        parsed = _parse_flowchart('graph TD\n    A["a"] --- B["b"]\n')
        assert parsed is not None
        assert parsed[4][0].label is None

    def test_chained_edge_expands_to_pairwise_edges(self):
        # Real Mermaid shorthand: `A --> B --> C` means A-->B plus B-->C.
        parsed = _parse_flowchart('graph TD\n    A["a"] --> B["b"] --> C["c"]\n')
        assert parsed is not None
        assert [(e.source, e.target) for e in parsed[4]] == [("A", "B"), ("B", "C")]

    def test_chained_edge_of_four_nodes(self):
        parsed = _parse_flowchart('graph TD\n    A["a"] --> B["b"] --> C["c"] --> D["d"]\n')
        assert parsed is not None
        assert [(e.source, e.target) for e in parsed[4]] == [("A", "B"), ("B", "C"), ("C", "D")]

    def test_chained_edge_with_mixed_arrow_styles_and_labels(self):
        parsed = _parse_flowchart('graph TD\n    A["a"] -->|"x"| B["b"] -.->|"y"| C["c"]\n')
        assert parsed is not None
        edges = parsed[4]
        assert (edges[0].source, edges[0].target, edges[0].label, edges[0].arrow) == ("A", "B", "x", "-->")
        assert (edges[1].source, edges[1].target, edges[1].label, edges[1].arrow) == ("B", "C", "y", "-.->")

    def test_two_node_edge_is_not_misrouted_into_the_chain_handler(self):
        # Regression guard: the ordinary single-arrow case must still be
        # handled by _try_simple_edge (already tested extensively above),
        # never fall through to the chain handler.
        parsed = _parse_flowchart('graph TD\n    A["a"] --> B["b"]\n')
        assert parsed is not None
        assert len(parsed[4]) == 1

    def test_bare_node_declaration_alone_on_a_line_is_valid(self):
        # Real Mermaid: `A` alone just touches the node (uses its id as the
        # label if never given one elsewhere) - not meaningless syntax.
        parsed = _parse_flowchart('graph TD\n    A\n    B["b"]\n    A --> B\n')
        assert parsed is not None
        assert "A" not in parsed[2]  # labels - never given one, so none recorded
        assert "A" in parsed[1].node_ids

    def test_asymmetric_flag_shape_is_parsed(self):
        parsed = _parse_flowchart('graph TD\n    A>"a"] --> B["b"]\n')
        assert parsed is not None
        assert parsed[2]["A"] == "a"
        assert parsed[3]["A"] == ">"

    def test_classdef_and_class_directives_are_skipped(self):
        # Presentation-only (real Mermaid, real llm-server test fixture
        # shape) - recognized and ignored rather than failing the diagram.
        code = 'graph TD\n    A["a"] --> B["b"]\n    classDef hl fill:#f9f,stroke:#333\n    class A hl;\n'
        parsed = _parse_flowchart(code)
        assert parsed is not None
        assert [(e.source, e.target) for e in parsed[4]] == [("A", "B")]

    def test_style_directive_is_skipped(self):
        code = 'graph TD\n    A["a"] --> B["b"]\n    style A fill:#f9f,stroke:#333\n'
        assert _parse_flowchart(code) is not None

    def test_click_directive_is_skipped(self):
        code = 'graph TD\n    A["a"] --> B["b"]\n    click A "https://example.com"\n'
        assert _parse_flowchart(code) is not None

    def test_linkstyle_directive_is_skipped(self):
        # Edge-styling counterpart to classDef - presentation only.
        code = 'graph TD\n    A["a"] --> B["b"]\n    linkStyle 0 stroke:#f00,stroke-width:2px;\n'
        parsed = _parse_flowchart(code)
        assert parsed is not None
        assert [(e.source, e.target) for e in parsed[4]] == [("A", "B")]

    def test_yaml_frontmatter_block_is_dropped(self):
        # Real Mermaid + llm-server's own validator accept a `---` YAML block
        # ahead of the diagram type; drop it wholesale, never interpreted.
        code = '---\ntitle: My Diagram\nconfig:\n  theme: dark\n---\ngraph TD\n    A["a"] --> B["b"]\n'
        parsed = _parse_flowchart(code)
        assert parsed is not None
        assert [(e.source, e.target) for e in parsed[4]] == [("A", "B")]

    def test_unterminated_frontmatter_fails_closed(self):
        code = '---\ntitle: X\ngraph TD\n    A["a"] --> B["b"]\n'
        assert _parse_flowchart(code) is None

    def test_parallelogram_and_trapezoid_shapes_fail_closed(self):
        # `[/ ... /]` `[\ ... \]` etc. are shapes this parser doesn't model;
        # they must NOT silently parse as a rect with the slashes stuck in
        # the label (a partial parse this module exists to avoid).
        assert _parse_flowchart('graph TD\n    A[/Process/] --> B["b"]\n') is None
        assert _parse_flowchart('graph TD\n    A[\\Note\\] --> B["b"]\n') is None
        assert _parse_flowchart('graph TD\n    A[/Trap\\] --> B["b"]\n') is None

    def test_inline_class_shorthand_on_node_decl_is_ignored(self):
        # `id:::className` - real Mermaid's inline class-shorthand, on nearly
        # every node of real generated diagrams. Consumed as part of the node
        # token and dropped (presentation only), not a parse failure.
        code = (
            'graph TD\n    subgraph S["Services"]\n'
            '        app["app-dev (2 replicas)"]:::running\n'
            '        db["db (1 replica)"]:::pending\n    end\n'
        )
        parsed = _parse_flowchart(code)
        assert parsed is not None
        assert parsed[2] == {"app": "app-dev (2 replicas)", "db": "db (1 replica)"}
        assert parsed[1].children[0].node_ids == ["app", "db"]

    def test_inline_class_shorthand_on_edge_endpoints_is_ignored(self):
        parsed = _parse_flowchart('graph TD\n    A["a"]:::hot --> B["b"]:::cold\n')
        assert parsed is not None
        assert [(e.source, e.target) for e in parsed[4]] == [("A", "B")]

    def test_trailing_inline_comment_is_stripped(self):
        parsed = _parse_flowchart('graph TD\n    A["a"] --> B["b"] %% note\n')
        assert parsed is not None
        assert [(e.source, e.target) for e in parsed[4]] == [("A", "B")]

    def test_literal_percent_percent_inside_a_quoted_label_is_preserved(self):
        # A "%%" naively truncates at the FIRST occurrence, even inside a
        # quoted label (e.g. a literal template placeholder or percentage
        # value) - only a "%%" outside any quotes is really a comment.
        parsed = _parse_flowchart('graph TD\n    A["50%%off"] --> B["b"]\n')
        assert parsed is not None
        assert parsed[2]["A"] == "50%%off"

    def test_percent_percent_comment_after_a_quoted_label_still_stripped(self):
        parsed = _parse_flowchart('graph TD\n    A["a"] --> B["b"] %% 50%% done\n')
        assert parsed is not None
        assert [(e.source, e.target) for e in parsed[4]] == [("A", "B")]

    def test_whole_line_comment_still_works(self):
        parsed = _parse_flowchart('graph TD\n    %% a comment\n    A["a"] --> B["b"]\n')
        assert parsed is not None

    def test_trailing_semicolon_statement_terminators_are_stripped(self):
        # Real Mermaid accepts a trailing ";" on every line; the
        # VisualizationAgent emits diagrams in this style. Each line must
        # still parse rather than failing the whole diagram to the raw
        # code-block fallback.
        code = (
            "graph LR;\n"
            '    subgraph "Namespace: demo-vanshika"\n'
            "        direction LR;\n"
            '        subgraph "Workloads"\n'
            "            direction LR;\n"
            '            ad["Service: ad"] --> flagd_evaluation_v2_service["flagd.evaluation.v2.Service"];\n'
            "        end;\n"
            "    end;\n"
        )
        parsed = _parse_flowchart(code)
        assert parsed is not None
        assert [(e.source, e.target) for e in parsed[4]] == [("ad", "flagd_evaluation_v2_service")]

    def test_semicolon_inside_a_quoted_label_is_preserved(self):
        parsed = _parse_flowchart('graph TD\n    A["a; still a"] --> B["b"];\n')
        assert parsed is not None
        assert parsed[2]["A"] == "a; still a"

    @pytest.mark.parametrize(
        "code",
        [
            "just plain text, not mermaid at all",
            "graph TD\n    A[a [b]] --> B[c]\n",  # same-family bracket char: still genuinely ambiguous
            "graph TD\n    A(a (b)) --> B[c]\n",  # rounded shape, same issue - "(" ")" ARE its own delimiter
            "graph TD\n",  # header only, no nodes
            'graph TD\n    A["a"] --> B["b"]\n    end\n',  # unmatched `end`
            'graph TD\n    subgraph "X"\n        A["a"] --> B["b"]\n',  # unclosed subgraph
            "sequenceDiagram\n    Alice->>Bob: Hi\n",  # not a supported diagram type at all
        ],
    )
    def test_unsupported_or_malformed_syntax_fails_closed(self, code):
        # The parser is deliberately all-or-nothing (see mermaid_graph.py's
        # module docstring): anything it doesn't fully understand must
        # return None, never a partial/best-effort graph.
        assert _parse_flowchart(code) is None

    def test_oversized_diagram_is_rejected(self):
        nodes = "\n".join(f'    N{i}["Node {i}"] --> N{i + 1}["Node {i + 1}"]' for i in range(_MAX_NODES + 10))
        code = f"flowchart TD\n{nodes}\n"
        assert _parse_flowchart(code) is None


class TestBuildGraphviz:
    def test_produces_valid_dot_source_with_cluster(self):
        parsed = _parse_flowchart(
            "graph TD\n"
            '    subgraph "Service Mesh"\n'
            '        S1["API Gateway"] --> S2["Auth Service"]\n'
            "    end\n"
        )
        rankdir, root, labels, shapes, edges = parsed
        dot = _build_graphviz(rankdir, root, labels, shapes, edges)
        source = dot.source
        assert "cluster_0" in source
        assert 'label="Service Mesh"' in source
        assert "S1 -> S2" in source

    def test_plain_arrow_gets_default_forward_edge(self):
        parsed = _parse_flowchart('graph TD\n    A["a"] --> B["b"]\n')
        dot = _build_graphviz(*parsed)
        assert "dir=both" not in dot.source
        assert "dir=none" not in dot.source
        assert "style=" not in dot.source

    def test_bidirectional_arrow_gets_dir_both(self):
        parsed = _parse_flowchart('graph TD\n    A["a"] <--> B["b"]\n')
        dot = _build_graphviz(*parsed)
        assert "dir=both" in dot.source

    def test_undirected_arrow_gets_dir_none(self):
        parsed = _parse_flowchart('graph TD\n    A["a"] --- B["b"]\n')
        dot = _build_graphviz(*parsed)
        assert "dir=none" in dot.source

    def test_dotted_arrow_gets_dashed_style(self):
        parsed = _parse_flowchart('graph TD\n    A["a"] -.-> B["b"]\n')
        dot = _build_graphviz(*parsed)
        assert "style=dashed" in dot.source

    def test_dotted_undirected_arrow_gets_both_attributes(self):
        parsed = _parse_flowchart('graph TD\n    A["a"] -.- B["b"]\n')
        dot = _build_graphviz(*parsed)
        assert "dir=none" in dot.source
        assert "style=dashed" in dot.source

    def test_thick_bidirectional_arrow_gets_dir_both_and_penwidth(self):
        parsed = _parse_flowchart('graph TD\n    A["a"] <==> B["b"]\n')
        dot = _build_graphviz(*parsed)
        assert "dir=both" in dot.source
        assert "penwidth=2" in dot.source

    def test_edge_label_survives_alongside_arrow_attributes(self):
        parsed = _parse_flowchart('graph TD\n    A["a"] <-->|"sync"| B["b"]\n')
        dot = _build_graphviz(*parsed)
        assert "dir=both" in dot.source
        assert "label=sync" in dot.source or 'label="sync"' in dot.source

    def test_edge_to_a_subgraph_clips_to_the_cluster_boundary(self):
        code = (
            "graph TD\n"
            '    Cluster["Kubernetes Cluster"]\n'
            '    subgraph NS ["Namespace: ingress-nginx"]\n'
            '        A["a"]\n'
            "    end\n"
            "    Cluster --> NS\n"
        )
        parsed = _parse_flowchart(code)
        dot = _build_graphviz(*parsed)
        source = dot.source
        # Terminates on the cluster's own real node (the anchor), clipped to
        # the cluster boundary via lhead - never a standalone "NS" node.
        assert "Cluster -> A" in source
        assert "lhead=cluster_0" in source
        assert "compound=true" in source
        assert "\tNS\n" not in source and "NS[" not in source

    def test_subgraph_edge_anchor_is_not_also_invisibly_chained(self):
        # Regression: connected_ids used to be computed from the raw,
        # unresolved edge endpoints ("NS", never a real node) instead of the
        # resolved anchor ("A"). That left A looking isolated to the
        # invis-chaining logic below, forcing it into an invisible chain
        # with its unrelated siblings B/C and distorting the layout, even
        # though A already has a real edge (Cluster --> NS, resolved to
        # Cluster --> A). B and C, genuinely isolated, still get chained.
        code = (
            "graph TD\n"
            '    Cluster["Kubernetes Cluster"]\n'
            '    subgraph NS ["Namespace"]\n'
            '        A["a"]\n'
            '        B["b"]\n'
            '        C["c"]\n'
            "    end\n"
            "    Cluster --> NS\n"
        )
        parsed = _parse_flowchart(code)
        dot = _build_graphviz(*parsed)
        source = dot.source
        assert "A -> B" not in source
        assert "B -> C [style=invis]" in source

    def test_edge_to_an_empty_subgraph_anchors_on_a_nested_child_node(self):
        # "Data & Caching Namespaces" style shape: a subgraph with no direct
        # nodes of its own, only nested sub-subgraphs that hold the real
        # nodes - the anchor must be found by recursing into children.
        code = (
            "graph TD\n"
            '    Cluster["Kubernetes Cluster"]\n'
            '    subgraph Data ["Data & Caching"]\n'
            '        subgraph NS_Redis ["Namespace: redis"]\n'
            '            Redis["redis-master"]\n'
            "        end\n"
            "    end\n"
            "    Cluster --> Data\n"
        )
        parsed = _parse_flowchart(code)
        dot = _build_graphviz(*parsed)
        assert "Cluster -> Redis" in dot.source

    def test_edge_free_sibling_nodes_get_chained_invisibly(self):
        # Regression: a subgraph that's just a flat resource listing (no
        # edges among its members at all - a real shape, e.g. every
        # Deployment in a namespace) used to collapse onto one Graphviz
        # rank, rendering as an extremely wide, thin strip instead of the
        # stacked column real Mermaid.js produces for this exact shape.
        code = (
            "graph TD\n"
            '    subgraph "Namespace"\n'
            '        A["a"]\n'
            '        B["b"]\n'
            '        C["c"]\n'
            "    end\n"
        )
        parsed = _parse_flowchart(code)
        dot = _build_graphviz(*parsed)
        assert "A -> B" in dot.source
        assert "B -> C" in dot.source
        assert "style=invis" in dot.source

    def test_nodes_with_real_edges_are_never_chained_invisibly(self):
        # The chaining must only ever apply to nodes with zero edges -
        # anything with real topology renders exactly as before.
        parsed = _parse_flowchart('graph TD\n    A["a"] --> B["b"]\n    B --> C["c"]\n')
        dot = _build_graphviz(*parsed)
        assert "style=invis" not in dot.source

    def test_isolated_sibling_subgraphs_get_chained_invisibly_too(self):
        # Regression: a diagram that's pure nesting with zero edges anywhere
        # (namespace -> workload listing, grouped into environments - a
        # real shape with no connecting arrows at all) still collapsed onto
        # one rank even after the bare-node chaining above, since that only
        # covers bare nodes, not whole sibling SUBGRAPHS with no edges
        # touching anything inside them. Chains each isolated subgraph's
        # last node to the next one's anchor (see _last_node_id), not
        # anchor-to-anchor - a cluster spans many ranks internally, so
        # nudging just its anchor by one rank doesn't stop its own (taller)
        # rank range from still overlapping the next cluster.
        code = (
            "graph TD\n"
            '    subgraph "Env A"\n'
            '        A1["a1"]\n'
            '        A2["a2"]\n'
            "    end\n"
            '    subgraph "Env B"\n'
            '        B1["b1"]\n'
            "    end\n"
        )
        parsed = _parse_flowchart(code)
        dot = _build_graphviz(*parsed)
        assert "A2 -> B1" in dot.source  # last of Env A -> anchor of Env B
        assert "weight=10" in dot.source

    def test_subgraph_with_a_real_edge_is_not_chained_to_its_siblings(self):
        # A subgraph that DOES have topology inside it is left alone - only
        # genuinely edge-free siblings join the chain.
        code = (
            "graph TD\n"
            '    subgraph "Env A"\n'
            '        A1["a1"] --> A2["a2"]\n'
            "    end\n"
            '    subgraph "Env B"\n'
            '        B1["b1"]\n'
            "    end\n"
        )
        parsed = _parse_flowchart(code)
        dot = _build_graphviz(*parsed)
        assert "A2 -> B1" not in dot.source

    def test_many_isolated_siblings_wrap_into_a_grid_not_one_long_chain(self):
        # 4 isolated siblings -> cols = round(sqrt(4)) = 2 rows of 2. Only
        # the row boundary (item 2 -> item 3) should be chained; items
        # within a row (1&2, 3&4) are left unconstrained so Graphviz's
        # default same-rank placement puts them side by side - a plain
        # single-file chain of all 4 would make an already-tall diagram
        # absurdly taller instead of balancing width against height.
        code = "graph TD\n" + "".join(f'    subgraph "Env {i}"\n        N{i}["n{i}"]\n    end\n' for i in range(1, 5))
        parsed = _parse_flowchart(code)
        dot = _build_graphviz(*parsed)
        assert "N2 -> N3" in dot.source  # the one row-boundary chain
        assert "N1 -> N2" not in dot.source
        assert "N3 -> N4" not in dot.source

    def test_grid_wrap_only_applies_at_the_shallowest_qualifying_level(self):
        # Wrapping EVERY level made things worse in testing (deeper sibling
        # groups tend to be smaller, where the width-for-height trade isn't
        # worth it) - once the outer level wraps into a real grid (cols>1),
        # nested levels stay a plain single-file chain even if they also
        # have enough isolated siblings to mathematically qualify.
        code = 'graph TD\n    subgraph "Outer"\n' + "".join(
            f'        subgraph "Inner {i}"\n            N{i}["n{i}"]\n        end\n' for i in range(1, 5)
        )
        code += "    end\n"
        parsed = _parse_flowchart(code)
        dot = _build_graphviz(*parsed)
        # The outer scope has only 1 child ("Outer" itself) - cols=1 there,
        # so the allowance passes through and "Outer"'s 4 Inner_i children
        # (the shallowest scope with enough isolated siblings) get the grid.
        assert "N2 -> N3" in dot.source
        assert "N1 -> N2" not in dot.source


class TestRenderFlowchartImage:
    def test_unparseable_code_returns_none_without_touching_graphviz(self):
        assert render_flowchart_image("not a flowchart") is None

    @pytest.mark.skipif(DOT_MISSING, reason="requires the `dot` binary (apk/apt-get install graphviz)")
    def test_valid_flowchart_renders_png_bytes(self):
        png = render_flowchart_image('graph TD\n    A["Start"] --> B["End"]\n')
        assert png is not None
        assert png[:8] == b"\x89PNG\r\n\x1a\n"  # PNG file signature

    @pytest.mark.skipif(DOT_MISSING, reason="requires the `dot` binary (apk/apt-get install graphviz)")
    def test_diagram_with_unquoted_subgraph_titles_renders(self):
        # The exact shape of a real diagram the frontend (real mermaid.js)
        # rendered correctly while this parser rejected it outright.
        code = (
            "flowchart LR\n"
            "    subgraph Frontend & API Layer\n"
            "        UI[app-dev UI]\n"
            "    end\n"
            "    subgraph Core Services\n"
            "        SVC[services Backend Server]\n"
            "    end\n"
            "    UI --> SVC\n"
        )
        png = render_flowchart_image(code)
        assert png is not None
        assert png[:8] == b"\x89PNG\r\n\x1a\n"  # PNG file signature

    @pytest.mark.skipif(DOT_MISSING, reason="requires the `dot` binary (apk/apt-get install graphviz)")
    def test_unquoted_label_with_parens_renders(self):
        # Real-world shape that motivated the shape-aware node-token regex:
        # a K8s-style label with a parenthetical aside, unquoted, in a rect
        # node - used to fail closed entirely.
        code = "graph TD\n    STS[StatefulSet] -->|Creates Ordered| P2[Pod: Stateful Instance (Ordinal)]\n"
        png = render_flowchart_image(code)
        assert png is not None
        assert png[:8] == b"\x89PNG\r\n\x1a\n"  # PNG file signature

    @pytest.mark.skipif(DOT_MISSING, reason="requires the `dot` binary (apk/apt-get install graphviz)")
    def test_edge_to_a_subgraph_still_renders(self):
        code = (
            "graph TD\n"
            '    Cluster["Kubernetes Cluster"]\n'
            '    subgraph NS ["Namespace: ingress-nginx"]\n'
            '        A["a"]\n'
            "    end\n"
            "    Cluster --> NS\n"
        )
        png = render_flowchart_image(code)
        assert png is not None
        assert png[:8] == b"\x89PNG\r\n\x1a\n"  # PNG file signature

    @pytest.mark.skipif(DOT_MISSING, reason="requires the `dot` binary (apk/apt-get install graphviz)")
    def test_flat_resource_listing_does_not_collapse_into_a_wide_strip(self):
        # A namespace-style flat listing (no edges among siblings, a real
        # shape) used to collapse onto one Graphviz rank - 12 same-rank
        # nodes side by side is an extremely wide, thin strip. Chaining them
        # invisibly (see _add_scope) stacks them into a column instead,
        # matching real Mermaid.js's own layout for this shape - verified
        # against a real 20-node diagram (56:1 -> ~4:1 aspect ratio).
        code = (
            'graph TD\n    subgraph "Namespace"\n'
            + "\n".join(f'        N{i}["Node {i}"]' for i in range(12))
            + "\n    end\n"
        )
        parsed = _parse_flowchart(code)
        dot = _build_graphviz(*parsed)
        dot.format = "plain"
        bbox_line = dot.pipe().decode().splitlines()[0]
        _, _scale, width, height = bbox_line.split()
        assert float(width) / float(height) < 3, f"expected a roughly column-shaped layout, got {bbox_line}"

    @pytest.mark.skipif(DOT_MISSING, reason="requires the `dot` binary (apk/apt-get install graphviz)")
    def test_pure_nesting_with_no_edges_anywhere_does_not_collapse_into_a_wide_strip(self):
        # Real shape that motivated the sibling-subgraph chaining: a pure
        # containment diagram (environments -> namespaces -> workload
        # listings) with zero edges anywhere in the whole file - verified
        # against a real 7-environment diagram (11.9:1 -> a tall, fully
        # readable single-column stack).
        code = "graph TD\n" + "".join(f'    subgraph "Env {i}"\n        N{i}["Node {i}"]\n    end\n' for i in range(6))
        parsed = _parse_flowchart(code)
        dot = _build_graphviz(*parsed)
        dot.format = "plain"
        bbox_line = dot.pipe().decode().splitlines()[0]
        _, _scale, width, height = bbox_line.split()
        assert float(width) / float(height) < 3, f"expected a roughly column-shaped layout, got {bbox_line}"

    @pytest.mark.skipif(DOT_MISSING, reason="requires the `dot` binary (apk/apt-get install graphviz)")
    def test_subgraph_with_direction_still_renders(self):
        # Regression: a `direction` line inside a subgraph used to fail the
        # whole parse (unrecognized line -> fail closed), even though it's
        # valid Mermaid syntax.
        code = (
            "graph TD\n"
            '    subgraph "Pipeline"\n'
            "        direction LR\n"
            '        A["Start"] --> B["End"]\n'
            "    end\n"
        )
        png = render_flowchart_image(code)
        assert png is not None
        assert png[:8] == b"\x89PNG\r\n\x1a\n"  # PNG file signature
