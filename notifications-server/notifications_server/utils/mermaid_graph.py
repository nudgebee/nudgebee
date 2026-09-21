"""Render Mermaid ``flowchart``/``graph`` diagrams as an actual image, via a
strict node/edge parser plus Graphviz - the Image tier in the Slack-native ->
Image -> code-block fallback chain (see mermaid_chart.py's render_mermaid_code).

Mermaid's full flowchart grammar (subgraphs, six-plus arrow styles, a dozen
node shapes, style/classDef/click directives, ...) is too large to safely
replicate, and the dangerous failure mode isn't "fails to parse" - that's
safe, it falls through to the existing code-block fallback - it's a
*partial* parse: silently dropping a subgraph or an edge label and still
rendering a clean-looking picture that misrepresents the real diagram. That
would be worse than today's raw-text fallback, which is at least honest
about what it shows. So this parser is deliberately all-or-nothing: any
line that isn't recognized fails the whole parse (returns None), and the
caller falls through to the code-block fallback exactly as it did before
this module existed.

Scope is intentionally narrow: the subset the VisualizationAgent's own prompt
documents and its validation tool enforces
(llm/llm-server/agents/agent_visualization.go,
llm/llm-server/tools/tool_mermaid_validation.go), extended in a few places
where real diagrams turned out to use more of real Mermaid's grammar than
that - node labels (quoted or unquoted, same tolerance mermaid_chart.py's
xychart/pie parsers already have, since real generated diagrams don't
always quote), subgraph titles (same quoted-or-unquoted tolerance),
`graph`/`flowchart` TD/TB/BT/RL/LR, `subgraph "Title"` / `subgraph ID
["Title"]` / unquoted `subgraph Title` blocks, an optional `direction
TD/TB/BT/RL/LR` line inside a subgraph, a bare `A` node-touch line, and
edges with either label spelling (`-->|"Label"|` or the dash-text
`-- Label -->`/`-- Label ---` form) in a small set of arrow styles
(including bidirectional and multi-arrow chains like `A --> B --> C`,
Mermaid's shorthand for A-->B plus B-->C). `direction` is accepted and
passed through to Graphviz, but Graphviz's `dot` engine doesn't actually
vary rank direction per cluster - verified empirically - so a subgraph's
internal layout may not rotate the way Mermaid itself would render it.
That's an accepted, honest tradeoff (accurate content, imperfect
sub-layout) rather than falling back to the code block over a directive we
can only partially honor.

An edge may also name a subgraph directly (`subgraph NS ["Namespace"]` ...
`Cluster --> NS`) - real Mermaid's documented way to point an edge at a
whole cluster rather than one node inside it. Handled properly, not
approximated: the edge is drawn to a real anchor node inside that subgraph
with Graphviz's `lhead`/`ltail` clipping it to the cluster's boundary
(https://graphviz.org/docs/attrs/lhead/), so it visually terminates at the
cluster, matching Mermaid. Getting this wrong silently (e.g. treating the
subgraph's id as a new, empty, disconnected node) is exactly the
"misrepresents the real diagram" failure mode this module exists to avoid.

`classDef`/`class`/`style`/`click`/`linkStyle` directives are recognized
and skipped rather than rejecting the whole diagram - they only affect
presentation (colors/tooltips/links), never topology or text, so dropping
them is a materially smaller risk than dropping a subgraph or a label. The
inline `id:::className` class-shorthand form (`app["app-dev"]:::running`,
which real generated diagrams use on nearly every node) is consumed and
discarded the same way, as part of the node token itself. A leading YAML
frontmatter block (`---` / `title: ...` / `---`, which real Mermaid and
llm-server's own validator both accept ahead of the diagram type) is
dropped wholesale, never interpreted.

Anything outside all of that (a label containing a literal bracket
character while unquoted, the slash/backslash-bracketed parallelogram and
trapezoid node shapes this parser doesn't model, diagram types it doesn't
target at all) fails closed rather than being approximated. Every
fail-closed path logs why (see _reject) so prod data can show which
unsupported cases actually occur.
"""

import logging
import re
from dataclasses import dataclass, field
from typing import Dict, List, Optional, Tuple

try:
    import graphviz
except ImportError:  # pragma: no cover - graphviz is a hard runtime dependency in prod
    graphviz = None

LOG = logging.getLogger(__name__)

# Diagram types this module can attempt to render as an image (everything
# else - classDiagram, erDiagram, gantt, sequenceDiagram, timeline, journey -
# stays on the code-block fallback; they either aren't graph-shaped or their
# Mermaid syntax is too different to share this parser).
SUPPORTED_GRAPH_TYPES = {"graph", "flowchart"}

# Safety caps so a pathological/adversarial diagram can't produce a huge
# image or take a long time to lay out - same spirit as mermaid_chart.py's
# _SLACK_MAX_* caps, but bounding render cost rather than Slack's limits.
_MAX_NODES = 200
_MAX_EDGES = 400

_DIRECTIONS = {"TD": "TB", "TB": "TB", "BT": "BT", "RL": "RL", "LR": "LR"}

_HEADER_RE = re.compile(r"^(?:graph|flowchart)\s+(TD|TB|BT|RL|LR)\s*$", re.IGNORECASE)
# Group 1: title for `subgraph "Title"`. Group 2: the id/text before the
# bracket in `subgraph ID [Title]` - the id is what a later edge line (e.g.
# `Cluster --> NS_Ingress`) uses to point at this whole cluster rather than a
# plain node, per real Mermaid's "edges to subgraphs" feature. Not restricted
# to a single word here (real Mermaid tolerates a multi-word id like
# `subgraph K8s Cluster [Title]` - verified against a live diagram) - only
# usable as an actual edge-referenceable id when it turns out to be a single
# word (see _subgraph_id_from_match), same as the bracket-free form below.
# Groups 3+4: the bracketed title itself, quoted or unquoted respectively -
# real Mermaid accepts either. Group 5: `subgraph Some Unquoted Title` with
# no quotes/brackets at all - real Mermaid.js accepts this fine too
# (verified against a live diagram whose titles all had spaces and "&"),
# same quoted-or-unquoted tolerance already given to node labels below. No
# id capture for this form since there's no bracket to hold one.
_SUBGRAPH_START_RE = re.compile(
    r'^subgraph\s+(?:"([^"]*)"|([^"\[\]]+?)\s*\[(?:"([^"]*)"|([^"\[\]]*))\]|([^"\[\(\{\)\}\]]+))\s*$'
)
_SUBGRAPH_END_RE = re.compile(r"^end\s*$")
_DIRECTION_RE = re.compile(r"^direction\s+(TD|TB|BT|RL|LR)\s*$", re.IGNORECASE)

# Directives real Mermaid supports but that only affect presentation
# (colors/tooltips/links), never topology or text content - recognized and
# skipped rather than rejecting the whole diagram over them. Content
# accuracy (nodes/edges/labels) is what this module protects; dropping pure
# styling is a materially smaller risk than dropping a subgraph or a label
# (see module docstring), and real diagrams do use these - llm-server's own
# test fixture for a "valid complex scenario"
# (tools/tool_mermaid_validation_test.go's TestComplexScenario) includes
# classDef/class lines. `linkStyle` is the edge-styling counterpart to
# `classDef` (`linkStyle 0 stroke:#f00,stroke-width:2px`) - same pure-styling
# risk profile, and the same diagrams that carry classDef/`:::` carry it.
_SKIPPED_DIRECTIVE_RE = re.compile(r"^(?:classDef|class|style|click|linkStyle)\s+\S.*$")

# Opening bracket -> required closing bracket, for the node shapes the
# VisualizationAgent's prompt documents, plus the asymmetric/"flag" shape
# (`id>Label]`) which it doesn't but real Mermaid supports. Order matters:
# _node_token_pattern below builds one alternation from this, so a 2-char
# opener (e.g. "((") must come before its 1-char prefix ("(").
_OPEN_TO_CLOSE = {
    "[(": ")]",  # cylinder
    "((": "))",  # circle
    "{{": "}}",  # hexagon
    "[[": "]]",  # subroutine
    "([": "])",  # stadium
    "[": "]",  # rect
    "(": ")",  # rounded
    "{": "}",  # rhombus
    ">": "]",  # asymmetric / flag
}
# Short identifier per shape, for building this shape's unique regex group
# names (Python's re requires globally-unique group names within one
# pattern - see _node_token_pattern).
_SHAPE_KEYS = {
    "[(": "cyl",
    "((": "circ",
    "{{": "hex",
    "[[": "sub",
    "([": "stad",
    "[": "rect",
    "(": "round",
    "{": "rhomb",
    ">": "flag",
}


def _unquoted_label_class(opener: str, closer: str) -> str:
    """The characters a given shape's UNQUOTED label may safely contain:
    anything except quotes and this shape's own opener/closer characters.
    A rect `[...]` label can safely hold a literal paren, since `(`/`)`
    aren't part of `[`/`]` - but a rounded `(...)` label can't, since a
    literal `)` would be indistinguishable from the real closer. Each shape
    gets its own class (see _node_token_pattern) rather than one shared
    class banning every bracket type everywhere, so e.g. a Kubernetes-style
    label like `Pod: Stateful Instance (Ordinal)` parses in a rect node
    without needing to be quoted."""
    excluded = {'"'} | set(opener) | set(closer)
    return "[^" + "".join(re.escape(c) for c in sorted(excluded)) + "]*"


def _node_token_pattern(suffix: str) -> str:
    r"""One node-token regex fragment: an id, optionally followed by a
    shape-bracketed label - quoted or unquoted (real-world diagrams don't
    always quote, same tolerance mermaid_chart.py's xychart/pie parsers
    already give). Built as one alternation of fully-paired (opener,
    label-class, closer) branches per shape, rather than one opener
    matched loosely against any run of closing-bracket characters
    afterward - that looser approach couldn't tell "this closing char
    belongs to the label" from "this is the real closer" once labels are
    allowed to contain other shapes' bracket characters, silently pairing
    e.g. a rect's "]" with a stray ")" from inside a label. Pairing is
    correct by construction here: whichever branch matches has already
    consumed its own exact opener and exact closer, nothing to verify
    afterward. `suffix` keeps one occurrence's group names unique when
    several node tokens are embedded in one larger pattern (e.g. both
    sides of an edge) - see _extract_node.

    The id itself is `\w+(?:-\w+)*` rather than plain `\w+` - real-world
    (especially Kubernetes-sourced) diagrams routinely use kebab-case ids
    like `cert-manager` or `actions-runner-system-1`, which `\w+` alone
    can't match at all (verified: a live diagram's entire node list used
    this style and failed to parse). Each hyphen must be immediately
    followed by another word character rather than allowing a bare `[\w-]+`
    run, so a hyphen that's actually the start of an unspaced arrow
    (`A-->B`, `A---B`) is never swallowed into the id - `A` stops the id
    there since the next `-` isn't followed by a word character, leaving
    `-->`/`---` intact for _EDGE_ARROW to match.

    A trailing `:::className` is real Mermaid's inline class-shorthand
    (`A["Label"]:::running`, also valid on either side of an edge) -
    consumed here and discarded, same as the standalone
    `classDef`/`class`/`style`/`click` directives (see
    _SKIPPED_DIRECTIVE_RE): presentation only, never topology or text.
    Baked into the node token itself rather than stripped line-by-line so
    it's handled wherever a node appears - node declarations and both ends
    of every edge form - without each handler needing to know about it."""
    branches = []
    for opener, closer in _OPEN_TO_CLOSE.items():
        key = _SHAPE_KEYS[opener] + suffix
        unquoted = _unquoted_label_class(opener, closer)
        branches.append(re.escape(opener) + f'(?:"(?P<q_{key}>[^"]*)"|(?P<u_{key}>{unquoted}))' + re.escape(closer))
    return rf"(?P<id_{suffix}>\w+(?:-\w+)*)(?:" + "|".join(branches) + r")?(?::::[\w-]+)?"


def _extract_node(match: re.Match, suffix: str) -> Tuple[str, Optional[str], Optional[str], Optional[str]]:
    """Pull (id, opener, quoted_label, unquoted_label) out of a match
    produced by _node_token_pattern(suffix) - scans for whichever shape's
    named group pair actually fired. None fired means a bare id with no
    shape at all (a reference back to a node declared elsewhere)."""
    node_id = match.group(f"id_{suffix}")
    for opener in _OPEN_TO_CLOSE:
        key = _SHAPE_KEYS[opener] + suffix
        qlabel = match.group(f"q_{key}")
        ulabel = match.group(f"u_{key}")
        if qlabel is not None or ulabel is not None:
            return node_id, opener, qlabel, ulabel
    return node_id, None, None, None


# Bidirectional arrows added because real diagrams use them, not because the
# prompt documents them. Named (not just captured) so it survives to
# _build_graphviz via _ARROW_GRAPHVIZ_ATTRS below - Graphviz's default edge
# is always a single forward arrowhead, so without this a "---"/"<-->" edge
# would silently render identical to a plain "-->".
_EDGE_ARROW = r"(?P<arrow><-->|<-\.->|<==>|-->|---|-\.->|-\.-|==>|===)"
# Mermaid arrow style -> Graphviz edge attributes. Omitted keys keep
# Graphviz's own default (dir=forward, solid line).
_ARROW_GRAPHVIZ_ATTRS = {
    "-->": {},
    "-.->": {"style": "dashed"},
    "==>": {"penwidth": "2"},
    "---": {"dir": "none"},
    "-.-": {"dir": "none", "style": "dashed"},
    "===": {"dir": "none", "penwidth": "2"},
    "<-->": {"dir": "both"},
    "<-.->": {"dir": "both", "style": "dashed"},
    "<==>": {"dir": "both", "penwidth": "2"},
}
# Edge label: quoted or unquoted, same convention as node labels - content
# excludes "|" so it can't swallow the closing pipe.
_EDGE_RE = re.compile(
    rf'^{_node_token_pattern("a")}\s*{_EDGE_ARROW}\s*'
    rf'(?:\|(?:"(?P<edge_qlabel>[^"]*)"|(?P<edge_ulabel>[^|]*))\|\s*)?{_node_token_pattern("b")}$'
)
_NODE_DECL_RE = re.compile(rf'^{_node_token_pattern("decl")}$')

# Real Mermaid's other edge-label spelling: text sits directly between two
# dash groups (`A -- Some Label --> B`) instead of after the arrow in pipes
# (`A -->|Some Label| B`) - both are valid, real diagrams use either.
# Confirmed real: llm-server's own "valid complex scenario" test fixture
# (tool_mermaid_validation_test.go's TestComplexScenario) uses this form
# throughout (`POD_1 -- Pulls Image From --> MODEL_REGISTRY`). Label is
# non-greedy so it stops at the first closing `-->`/`---` rather than
# swallowing the rest of the line; only tried after _EDGE_RE fails, so a
# plain unlabeled `---`/`-->` is unaffected (already matches there).
_DASH_LABEL_EDGE_RE = re.compile(
    rf'^{_node_token_pattern("a")}\s*--\s+'
    rf'(?:"(?P<dash_qlabel>[^"]*)"|(?P<dash_ulabel>.+?))\s+'
    rf'(?P<dash_closer>-->|---)\s*{_node_token_pattern("b")}$'
)

# Unanchored variants of the node token / edge-arrow-and-label shape, used
# to walk a chained multi-arrow line (`A --> B --> C`, real Mermaid
# shorthand for A-->B plus B-->C, arbitrary length, styles may differ per
# segment) position by position via re.match(line, pos) - `^` would only
# ever match at pos 0 (a real Python re gotcha, not MULTILINE-related), so
# these intentionally have no anchors at all; the caller's bookkeeping
# (matching consecutive segments with no gap) supplies the anchoring
# instead. See _try_chained_edge.
_NODE_TOKEN_RE = re.compile(_node_token_pattern("start"))
_CHAIN_SEGMENT_RE = re.compile(
    rf'\s*{_EDGE_ARROW}\s*(?:\|(?:"(?P<edge_qlabel>[^"]*)"|(?P<edge_ulabel>[^|]*))\|\s*)?{_node_token_pattern("seg")}'
)

# Mermaid's compound-node edge syntax: either side of an arrow may list
# several nodes joined by " & " (e.g. `A & B --> C` means both A-->C and
# B-->C; `A & B --> C & D` means all four combinations). Captured loosely
# here (each side as raw text, split and validated by _compound_side_tokens
# below) rather than trying to repeat a node token an unknown number of
# times in one regex, since Python's re only keeps the last match of a
# repeated group anyway. Non-greedy on the left so it stops at the FIRST
# arrow, in case a quoted label elsewhere on the line happens to contain "&".
_COMPOUND_EDGE_RE = re.compile(rf'^(.+?)\s*{_EDGE_ARROW}\s*(?:\|(?:"([^"]*)"|([^|]*))\|\s*)?(.+)$')
_AMP_SPLIT_RE = re.compile(r"\s*&\s*")

_SHAPE_TO_GRAPHVIZ = {
    "[": "box",
    "(": "ellipse",
    "([": "box",  # stadium - Graphviz has no true stadium shape
    "{{": "hexagon",
    "[[": "box",  # subroutine - closest built-in approximation
    "[(": "cylinder",
    "((": "doublecircle",
    "{": "diamond",
    ">": "note",  # asymmetric/flag - Graphviz's folded-corner shape is the closest analog
}


@dataclass
class _Edge:
    source: str
    target: str
    label: Optional[str]
    arrow: str


@dataclass
class _Subgraph:
    title: str
    node_ids: List[str] = field(default_factory=list)
    children: List["_Subgraph"] = field(default_factory=list)
    direction: Optional[str] = None
    # The `ID` in `subgraph ID ["Title"]` - None for the `subgraph "Title"`
    # form, which has no id an edge could reference. Used to resolve an edge
    # that names this subgraph directly (see _build_graphviz).
    subgraph_id: Optional[str] = None
    # Set by _add_scope once this cluster is emitted, e.g. "cluster_3" -
    # needed at edge-build time to point an edge at the cluster's boundary
    # (Graphviz's lhead/ltail) instead of a specific node inside it.
    graphviz_cluster_name: Optional[str] = None


def _register_node(node_id, opener, quoted_label, unquoted_label, labels, shapes, placed, scope, subgraph_ids) -> bool:
    """Record a node's label/shape (if this mention carries one) and, on its
    first mention anywhere, place it in the currently open subgraph scope
    for clustering. Opener/closer pairing no longer needs verifying here -
    _node_token_pattern only ever matches a shape's own exact closer, so a
    mismatch (e.g. "[..." closed with "}") simply can't be represented by a
    successful match at all.

    If `node_id` is actually a subgraph's own id (real Mermaid lets an edge
    name a subgraph directly, e.g. `Cluster --> NS_Ingress` where NS_Ingress
    is `subgraph NS_Ingress [...]`), it's deliberately NOT registered as a
    node here - _build_graphviz resolves it to the cluster's boundary
    instead. Giving it a shape/label (`opener is not None`) is a genuine
    ambiguity our simplified model can't safely resolve, so that fails
    closed rather than guessing."""
    if node_id in subgraph_ids:
        return opener is None
    # `[/ ... /]` `[\ ... \]` `[/ ... \]` `[\ ... /]` are real Mermaid's
    # parallelogram/trapezoid shapes - not in _OPEN_TO_CLOSE, so an unquoted
    # one matches the plain rect `[` branch instead, with the leading/trailing
    # slash swallowed into the label text. That's a silent partial parse (wrong
    # shape, corrupted label) - exactly what this module exists to avoid - so
    # fail closed on it rather than render a misleading rect.
    if opener == "[" and quoted_label is None and unquoted_label:
        stripped = unquoted_label.strip()
        if stripped and stripped[0] in "/\\" and stripped[-1] in "/\\":
            return False
    if opener is not None:
        label = quoted_label if quoted_label is not None else unquoted_label
        labels[node_id] = label.replace("<br/>", "\n").replace("<br>", "\n")
        shapes[node_id] = opener
    if node_id not in placed:
        placed.add(node_id)
        scope.node_ids.append(node_id)
    return True


def _compound_side_tokens(raw_side: str) -> Optional[List[Tuple]]:
    """Split one side of a Mermaid `A & B --> ...` compound-node edge into
    its individual node tokens' (id, opener, qlabel, ulabel) tuples (see
    _extract_node), or None if any `&`-separated piece isn't a single valid
    node token on its own."""
    pieces = _AMP_SPLIT_RE.split(raw_side.strip())
    tokens = []
    for piece in pieces:
        piece = piece.strip()
        if not piece:
            return None
        match = _NODE_DECL_RE.match(piece)
        if not match:
            return None
        tokens.append(_extract_node(match, "decl"))
    return tokens


# Sentinel returned by each _try_* line handler below to mean "this line
# isn't shaped like what I handle - try the next handler", distinct from
# True/False (matched, and succeeded/failed). Keeps _parse_flowchart's main
# loop a flat sequence of "try this shape, then that shape, ..." instead of
# one large branching function - each handler owns its own regex groups and
# _register_node calls.
_NOT_MATCHED = object()


def _try_simple_edge(line: str, labels, shapes, placed, scope, edges: List[_Edge], subgraph_ids):
    """One node on each side of the arrow - the common case."""
    edge_match = _EDGE_RE.match(line)
    if not edge_match:
        return _NOT_MATCHED
    a_id, a_open, a_qlabel, a_ulabel = _extract_node(edge_match, "a")
    b_id, b_open, b_qlabel, b_ulabel = _extract_node(edge_match, "b")
    ok = _register_node(a_id, a_open, a_qlabel, a_ulabel, labels, shapes, placed, scope, subgraph_ids)
    ok = ok and _register_node(b_id, b_open, b_qlabel, b_ulabel, labels, shapes, placed, scope, subgraph_ids)
    if not ok:
        return False
    edge_qlabel, edge_ulabel = edge_match.group("edge_qlabel"), edge_match.group("edge_ulabel")
    edge_label = edge_qlabel if edge_qlabel is not None else (edge_ulabel or None)
    edges.append(_Edge(a_id, b_id, edge_label, edge_match.group("arrow")))
    return len(edges) <= _MAX_EDGES


def _try_node_decl(line: str, labels, shapes, placed, scope, edges: List[_Edge], subgraph_ids):
    """A standalone node declaration, no arrow - `A["Label"]` to introduce a
    node's shape/label ahead of referencing it elsewhere, or a bare `A`
    alone (valid real Mermaid: just touches the node, using its id as the
    label if it's never given one - same as an unbracketed reference on an
    edge line already does via _register_node). Takes `edges` only to match
    the other handlers' signature for _dispatch_line's uniform call - a
    node declaration never adds one."""
    node_match = _NODE_DECL_RE.match(line)
    if not node_match:
        return _NOT_MATCHED
    node_id, opener, qlabel, ulabel = _extract_node(node_match, "decl")
    return _register_node(node_id, opener, qlabel, ulabel, labels, shapes, placed, scope, subgraph_ids)


def _try_dash_label_edge(line: str, labels, shapes, placed, scope, edges: List[_Edge], subgraph_ids):
    """`A -- Some Label --> B` / `A -- Some Label --- B` (see
    _DASH_LABEL_EDGE_RE above) - the dash-text edge-label spelling, only
    reached once _try_simple_edge has failed (so a plain unlabeled
    `---`/`-->` is already handled there)."""
    edge_match = _DASH_LABEL_EDGE_RE.match(line)
    if not edge_match:
        return _NOT_MATCHED
    a_id, a_open, a_qlabel, a_ulabel = _extract_node(edge_match, "a")
    b_id, b_open, b_qlabel, b_ulabel = _extract_node(edge_match, "b")
    ok = _register_node(a_id, a_open, a_qlabel, a_ulabel, labels, shapes, placed, scope, subgraph_ids)
    ok = ok and _register_node(b_id, b_open, b_qlabel, b_ulabel, labels, shapes, placed, scope, subgraph_ids)
    if not ok:
        return False
    edge_qlabel, edge_ulabel = edge_match.group("dash_qlabel"), edge_match.group("dash_ulabel")
    edge_label = edge_qlabel if edge_qlabel is not None else edge_ulabel.strip()
    arrow = "-->" if edge_match.group("dash_closer") == "-->" else "---"
    edges.append(_Edge(a_id, b_id, edge_label or None, arrow))
    return len(edges) <= _MAX_EDGES


def _try_chained_edge(line: str, labels, shapes, placed, scope, edges: List[_Edge], subgraph_ids):
    """`A --> B --> C` (arbitrary length, arrow style/label may differ per
    segment) - real Mermaid shorthand for A-->B plus B-->C. Only reached
    once _try_simple_edge has failed, so this never fires for the common
    single-arrow case; walks the line segment by segment with re.match(line,
    pos) rather than one regex, since a repeated group in Python's re only
    keeps its last match (same reason _compound_side_tokens exists)."""
    start = _NODE_TOKEN_RE.match(line)
    if not start:
        return _NOT_MATCHED
    node_tokens = [_extract_node(start, "start")]
    segments = []  # (arrow, edge_qlabel, edge_ulabel)
    pos = start.end()
    while pos < len(line):
        seg = _CHAIN_SEGMENT_RE.match(line, pos)
        if not seg:
            return _NOT_MATCHED  # doesn't look like a chain at all
        segments.append((seg.group("arrow"), seg.group("edge_qlabel"), seg.group("edge_ulabel")))
        node_tokens.append(_extract_node(seg, "seg"))
        pos = seg.end()
    if len(node_tokens) < 3:
        return _NOT_MATCHED  # exactly one arrow - _try_simple_edge already owns this shape

    ok = True
    for token in node_tokens:
        ok = ok and _register_node(*token, labels, shapes, placed, scope, subgraph_ids)
    if not ok:
        return False
    for i, (arrow, edge_qlabel, edge_ulabel) in enumerate(segments):
        edge_label = edge_qlabel if edge_qlabel is not None else (edge_ulabel or None)
        edges.append(_Edge(node_tokens[i][0], node_tokens[i + 1][0], edge_label, arrow))
    return len(edges) <= _MAX_EDGES


def _try_compound_edge(line: str, labels, shapes, placed, scope, edges: List[_Edge], subgraph_ids):
    """`A & B --> C & D` (see _COMPOUND_EDGE_RE above) - only reached once
    _try_simple_edge has already failed to match the whole line, so a quoted
    label containing a literal "&" (e.g. "Foo & Bar") is unaffected; it
    already matched as a simple edge."""
    if "&" not in line:
        return _NOT_MATCHED
    compound_match = _COMPOUND_EDGE_RE.match(line)
    if not compound_match:
        return _NOT_MATCHED
    left_raw, arrow, edge_qlabel, edge_ulabel, right_raw = compound_match.groups()
    left_tokens = _compound_side_tokens(left_raw)
    right_tokens = _compound_side_tokens(right_raw)
    if not (left_tokens and right_tokens):
        return _NOT_MATCHED
    ok = True
    for token in left_tokens + right_tokens:
        ok = ok and _register_node(*token, labels, shapes, placed, scope, subgraph_ids)
    if not ok:
        return False
    edge_label = edge_qlabel if edge_qlabel is not None else (edge_ulabel or None)
    for left_token in left_tokens:
        for right_token in right_tokens:
            edges.append(_Edge(left_token[0], right_token[0], edge_label, arrow))
    return len(edges) <= _MAX_EDGES


_LINE_HANDLERS = (_try_simple_edge, _try_dash_label_edge, _try_chained_edge, _try_node_decl, _try_compound_edge)


def _dispatch_line(line: str, labels, shapes, placed, scope, edges: List[_Edge], subgraph_ids) -> bool:
    """Try each edge/node-declaration line shape in turn. Returns True once
    one handler matches and succeeds, False if none matched (unrecognized -
    fail closed) or a matching handler found the line invalid (e.g. a
    bracket mismatch, or the edge cap exceeded)."""
    for handler in _LINE_HANDLERS:
        result = handler(line, labels, shapes, placed, scope, edges, subgraph_ids)
        if result is not _NOT_MATCHED:
            return bool(result)
    return False


def _strip_comment(line: str) -> str:
    """Truncate `line` at the first `%%` that starts a comment - real
    Mermaid allows one even when it's not the first thing on the line
    (`A --> B %% note`) - while leaving a `%%` inside a double-quoted label
    alone (e.g. a literal "50%%" in a quoted string). Tracks quote parity
    across the `%%`-delimited pieces: a `%%` only counts as a comment once
    every quote opened so far has been closed."""
    pieces = line.split("%%")
    kept = []
    in_quotes = False
    for piece in pieces:
        kept.append(piece)
        in_quotes ^= piece.count('"') % 2 == 1
        if not in_quotes:
            break
    return "%%".join(kept)


# Despite the name, this also accepts kebab-case (e.g. `cert-manager`) -
# matches the same `\w+(?:-\w+)*` tolerance _node_token_pattern gives node
# ids, so a kebab-case subgraph id isn't rejected while an identically-styled
# node id is accepted.
_SINGLE_WORD_RE = re.compile(r"^\w+(?:-\w+)*$")


def _subgraph_id_from_match(sub_start: "re.Match") -> Optional[str]:
    """The id an edge could use to reference this subgraph: the explicit
    bracketed `subgraph ID [Title]` form's pre-bracket text, when it's a
    single word (a multi-word id like `subgraph K8s Cluster [Title]` parses
    fine as a title-only form - see _SUBGRAPH_START_RE - but can't be an
    edge's target token, same as a multi-word bracket-free title below), or
    - matching real Mermaid, where the simplest subgraph form is just
    `subgraph NS` with no title at all, and NS doubles as both the id and
    the displayed title - a single-word unquoted title. A multi-word
    unquoted title (`subgraph Frontend & API Layer`) has no id; nothing to
    reference it by."""
    if sub_start.group(2):
        candidate = sub_start.group(2).strip()
        return candidate if _SINGLE_WORD_RE.match(candidate) else None
    unquoted_title = (sub_start.group(5) or "").strip()
    return unquoted_title if _SINGLE_WORD_RE.match(unquoted_title) else None


def _reject(reason: str, line: Optional[str] = None) -> None:
    """Record why a diagram fell back to the raw code block. Logged at INFO
    (not a warning - the fallback is the designed safe path, nothing is
    broken) so prod data can show which unsupported-syntax cases actually
    occur, and whether this strict subset is worth expanding, instead of the
    fallback being invisible."""
    if line is not None:
        LOG.info("mermaid_graph: falling back to code block (%s): %r", reason, line[:200])
    else:
        LOG.info("mermaid_graph: falling back to code block (%s)", reason)


def _strip_yaml_frontmatter(lines: List[str]) -> Optional[List[str]]:
    """Drop a leading YAML frontmatter block (`---` / ... / `---`) that real
    Mermaid - and llm-server's own validator (tools/tool_mermaid_validation.go)
    - allow ahead of the diagram type, carrying a title/config. Never
    interpreted, same as the styling directives. Returns the remaining lines,
    or None (already logged) if the block is malformed - an opening fence with
    no close, or nothing after it."""
    if not lines or lines[0] != "---":
        return lines
    try:
        close = lines.index("---", 1)
    except ValueError:
        _reject("unterminated frontmatter")
        return None
    remaining = lines[close + 1 :]
    if not remaining:
        _reject("frontmatter only, no diagram")
        return None
    return remaining


def _parse_flowchart(
    code: str,
) -> Optional[Tuple[str, _Subgraph, Dict[str, str], Dict[str, str], List[_Edge]]]:
    """Strictly parse Mermaid flowchart/graph syntax into a render-ready
    shape, or None if any line isn't recognized - see the module docstring
    for why this fails closed instead of best-effort."""
    # Strip Mermaid's optional trailing ";" (`graph LR;`, `end;`) - the
    # per-line handlers match bare syntax and one stray ";" fails the parse.
    lines = [_strip_comment(line).strip().rstrip(";").strip() for line in code.splitlines()]
    lines = [line for line in lines if line]
    if not lines:
        _reject("empty")
        return None

    lines = _strip_yaml_frontmatter(lines)
    if lines is None:
        return None

    header = _HEADER_RE.match(lines[0])
    if not header:
        _reject("unrecognized header", lines[0])
        return None
    rankdir = _DIRECTIONS[header.group(1).upper()]

    # Pre-scan for every `subgraph ID [...]` id declared anywhere in the
    # diagram, regardless of order, so an edge line can tell "this token is
    # a subgraph reference" from "this token is a node" - a subgraph can be
    # referenced by an edge line above its own declaration in real Mermaid.
    subgraph_ids = set()
    for line in lines[1:]:
        m = _SUBGRAPH_START_RE.match(line)
        if m and (sid := _subgraph_id_from_match(m)):
            subgraph_ids.add(sid)

    root = _Subgraph(title="")
    stack: List[_Subgraph] = [root]
    labels: Dict[str, str] = {}
    shapes: Dict[str, str] = {}
    placed: set = set()
    edges: List[_Edge] = []

    for line in lines[1:]:
        sub_start = _SUBGRAPH_START_RE.match(line)
        if sub_start:
            if sub_start.group(1) is not None:
                title = sub_start.group(1)
            elif sub_start.group(2) is not None:
                # Bracket form: prefer the bracketed title (quoted or not);
                # an empty bracket (`ID []`) falls back to the id/text itself.
                bracket_title = sub_start.group(3) if sub_start.group(3) is not None else sub_start.group(4)
                title = bracket_title if bracket_title else sub_start.group(2).strip()
            else:
                title = (sub_start.group(5) or "").strip()
            sg = _Subgraph(title=title, subgraph_id=_subgraph_id_from_match(sub_start))
            stack[-1].children.append(sg)
            stack.append(sg)
            continue

        if _SUBGRAPH_END_RE.match(line):
            if len(stack) == 1:
                _reject("unmatched end")
                return None
            stack.pop()
            continue

        direction_match = _DIRECTION_RE.match(line)
        if direction_match:
            stack[-1].direction = _DIRECTIONS[direction_match.group(1).upper()]
            continue

        if _SKIPPED_DIRECTIVE_RE.match(line):
            continue

        if not _dispatch_line(line, labels, shapes, placed, stack[-1], edges, subgraph_ids):
            _reject("unrecognized line", line)  # unrecognized, or matched but invalid - fail closed
            return None

    if len(stack) != 1:
        _reject("unclosed subgraph")
        return None
    if not placed or len(placed) > _MAX_NODES:
        _reject("no nodes" if not placed else f"too many nodes ({len(placed)} > {_MAX_NODES})")
        return None

    return rankdir, root, labels, shapes, edges


def _add_scope(
    dot,
    scope: _Subgraph,
    labels: Dict[str, str],
    shapes: Dict[str, str],
    cluster_counter: List[int],
    connected_ids: set,
    allow_grid_wrap: bool = True,
) -> None:
    for node_id in scope.node_ids:
        label = labels.get(node_id, node_id)
        shape = _SHAPE_TO_GRAPHVIZ.get(shapes.get(node_id, "["), "box")
        dot.node(node_id, label=label, shape=shape)

    # Nodes with no edges at all (real diagrams often just list resources
    # inside a subgraph with nothing connecting them) have nothing to give
    # Graphviz a rank order, so `dot` collapses them all onto one rank -
    # for a cluster of a dozen-plus such nodes that's an extremely wide,
    # thin strip rather than the stacked column real Mermaid.js renders.
    # Chaining them with invisible edges in declaration order costs nothing
    # visually (invisible, no label) but gives each one a distinct rank,
    # matching Mermaid.js's own layout for this exact shape (verified
    # against a real diagram: 56:1 -> ~4:1 aspect ratio). Scoped to this
    # cluster only - never touches nodes that already have real edges, so
    # diagrams with real topology are laid out exactly as before. Always a
    # single column, unlike the sibling-subgraph grid-wrap below - these are
    # individual, usually-short items (a label per line), where extra
    # columns of long text add more width than the height they save.
    isolated = [n for n in scope.node_ids if n not in connected_ids]
    for source_id, target_id in zip(isolated, isolated[1:]):
        dot.edge(source_id, target_id, style="invis")

    # Same idea, one level up: sibling subgraphs with no edges connecting
    # ANY of their (possibly deeply nested) nodes to anything else collapse
    # onto one rank exactly like bare isolated nodes do - a real diagram can
    # be pure nesting with zero edges anywhere (namespace -> workload
    # listings, grouped into environments), and 7 such environment clusters
    # side by side is as wide a strip as 20 bare nodes were. Unlike bare
    # nodes, a subgraph is a whole cluster with its own internal height, so
    # a plain single-file chain here just trades "too wide" for "absurdly
    # tall" (verified: a real 7-cluster diagram went from 11.9:1 to a
    # single ~65-tall column). Wrapping into a roughly square GRID of
    # clusters instead - chaining only every _cols_-th pair (row boundaries)
    # and leaving clusters within a row unconstrained, so Graphviz's default
    # same-rank placement puts them side by side - balances width against
    # height (same diagram: 11.9:1 -> ~1:1). Only applied at the shallowest
    # qualifying level (see allow_grid_wrap): wrapping EVERY level made
    # things worse in testing, since deeper sibling groups tend to be
    # smaller/shorter, where the same width-for-height trade isn't worth
    # it. A subgraph that DOES have a real edge somewhere inside it is left
    # alone, laid out by that edge's own topology same as before. Chains
    # each row boundary's LAST node (see _last_node_id) to the next row's
    # first/anchor node, not anchor-to-anchor - a cluster can span many
    # ranks internally, so nudging just its anchor by one rank still leaves
    # the rest of its (taller) rank range overlapping the next cluster,
    # which Graphviz then resolves by placing them side by side anyway.
    # Still the same plain sequential invisible-edge technique proven safe
    # for bare nodes above, deliberately NOT the `{rank=same}` grouping
    # across cluster boundaries that crashes Graphviz outright (see project
    # memory).
    isolated_children = [c for c in scope.children if not _scope_touches_connected(c, connected_ids)]
    cols = max(1, round(len(isolated_children) ** 0.5)) if allow_grid_wrap else 1
    child_allow_grid_wrap = allow_grid_wrap and cols == 1

    for child in scope.children:
        cluster_name = f"cluster_{cluster_counter[0]}"
        with dot.subgraph(name=cluster_name) as c:
            child.graphviz_cluster_name = cluster_name
            cluster_counter[0] += 1
            if child.title:
                c.attr(label=child.title)
            if child.direction:
                # Graphviz's `dot` layout engine does not actually vary rank
                # direction per cluster - verified empirically: a cluster's
                # nodes still rank in the parent graph's direction regardless
                # of this attribute. Set it anyway (harmless, and correct
                # per the DOT language) rather than rejecting the diagram
                # over a directive we can only partially honor - the
                # content (nodes/edges/labels) stays fully accurate, only
                # that one cluster's internal layout may not rotate the way
                # Mermaid itself would render it.
                c.attr(rankdir=child.direction)
            _add_scope(c, child, labels, shapes, cluster_counter, connected_ids, child_allow_grid_wrap)

    for i in range(cols - 1, len(isolated_children) - 1, cols):
        prev_bottom = _last_node_id(isolated_children[i])
        next_top = _anchor_node_id(isolated_children[i + 1])
        if prev_bottom is not None and next_top is not None:
            dot.edge(prev_bottom, next_top, style="invis", weight="10")


def _scope_touches_connected(scope: _Subgraph, connected_ids: set) -> bool:
    """Whether any node anywhere inside `scope`, including nested children,
    participates in a real edge - decides whether a sibling subgraph joins
    the isolated-subgraph invisible chain above."""
    if any(n in connected_ids for n in scope.node_ids):
        return True
    return any(_scope_touches_connected(child, connected_ids) for child in scope.children)


def _index_subgraphs_by_id(scope: _Subgraph, out: Dict[str, _Subgraph]) -> None:
    """Walk the whole scope tree collecting subgraph_id -> _Subgraph, so an
    edge endpoint that names a subgraph (see _register_node) can be resolved
    to the actual cluster it refers to."""
    if scope.subgraph_id:
        out[scope.subgraph_id] = scope
    for child in scope.children:
        _index_subgraphs_by_id(child, out)


def _anchor_node_id(scope: _Subgraph) -> Optional[str]:
    """A real node id to physically terminate an edge at when it's pointed
    at `scope` itself (Graphviz's lhead/ltail then clip the drawn line to
    the cluster's boundary instead of this specific node - see
    https://graphviz.org/docs/attrs/lhead/). Recurses into children since a
    subgraph that only groups other subgraphs (e.g. no nodes of its own)
    has no direct node to anchor on."""
    if scope.node_ids:
        return scope.node_ids[0]
    for child in scope.children:
        anchor = _anchor_node_id(child)
        if anchor:
            return anchor
    return None


def _last_node_id(scope: _Subgraph) -> Optional[str]:
    """The counterpart to _anchor_node_id: the last real node in `scope`'s
    own stacking order (children after bare nodes, matching _add_scope's
    render order - see the isolated-sibling-subgraph chaining there). A
    scope this deep can span many ranks internally (its own bare nodes and
    children were already chained top-to-bottom), so pairing THIS with the
    next sibling's _anchor_node_id - not anchor-to-anchor - is what actually
    forces the next sibling's whole cluster below this one instead of just
    nudging one node by a single rank while the rest of this cluster's
    (much taller) rank range still overlaps it."""
    if scope.children:
        last = _last_node_id(scope.children[-1])
        if last:
            return last
    if scope.node_ids:
        return scope.node_ids[-1]
    return None


def _resolve_endpoint(node_id: str, subgraphs_by_id: Dict[str, _Subgraph]) -> Tuple[str, Optional[_Subgraph]]:
    """Shared by both the connected_ids computation and the lhead/ltail
    edge-drawing below, so they agree on what an edge naming a subgraph
    actually touches."""
    subgraph = subgraphs_by_id.get(node_id)
    if subgraph is None:
        return node_id, None
    anchor = _anchor_node_id(subgraph)
    return (anchor, subgraph) if anchor is not None else (node_id, None)


def _build_graphviz(rankdir: str, root: _Subgraph, labels: Dict[str, str], shapes: Dict[str, str], edges: List[_Edge]):
    dot = graphviz.Digraph(format="png")
    dot.attr(rankdir=rankdir)
    # Graphviz's PNG default is 96 DPI. Slack generates its own scaled-down
    # thumbnail for an inline image, and at 96 DPI that downscale can clip a
    # pixel row off small text's ascenders/details (seen live: the top of a
    # capital "P" partially cut off in a rendered node label). Rendering at
    # a higher source resolution gives Slack's thumbnailer more detail to
    # downscale from, which is the standard fix for this - verified the
    # raw (unscaled) 96 DPI PNG itself does NOT clip anything, so the loss
    # happens downstream in Slack's own scaling, not in this render.
    dot.attr(dpi="150")
    dot.attr("node", fontname="Helvetica", fontsize="11")
    dot.attr("edge", fontname="Helvetica", fontsize="10")

    # Built up front (doesn't need graphviz_cluster_name, which _add_scope
    # only fills in below - _anchor_node_id/_resolve_endpoint just walk the
    # already-parsed node_ids/children tree) so an edge naming a subgraph
    # (e.g. `Cluster --> NS`) can be resolved to its real anchor node before
    # computing connected_ids. Getting this wrong - counting the literal,
    # never-registered "NS" as connected instead of the anchor - left the
    # anchor looking isolated to _add_scope's invis-chaining below, forcing
    # it into an invisible chain with unrelated sibling nodes and distorting
    # the layout even though it already has a real edge.
    subgraphs_by_id: Dict[str, _Subgraph] = {}
    _index_subgraphs_by_id(root, subgraphs_by_id)

    connected_ids = set()
    for edge in edges:
        source, _ = _resolve_endpoint(edge.source, subgraphs_by_id)
        target, _ = _resolve_endpoint(edge.target, subgraphs_by_id)
        connected_ids.add(source)
        connected_ids.add(target)

    _add_scope(dot, root, labels, shapes, [0], connected_ids)

    if subgraphs_by_id:
        # Required by Graphviz for lhead/ltail to take effect at all.
        dot.attr(compound="true")

    for edge in edges:
        attrs = dict(_ARROW_GRAPHVIZ_ATTRS.get(edge.arrow, {}))
        if edge.label:
            attrs["label"] = edge.label

        # An edge naming a subgraph must terminate on a real node for
        # Graphviz to draw anything - lhead/ltail then clip the line to the
        # cluster's boundary instead of that node. If the subgraph turns out
        # to have no real node anywhere inside it (degenerate - empty nested
        # subgraphs only), _resolve_endpoint leaves it as the bare id, same
        # as before this feature existed, rather than dropping the edge.
        source, source_subgraph = _resolve_endpoint(edge.source, subgraphs_by_id)
        target, target_subgraph = _resolve_endpoint(edge.target, subgraphs_by_id)
        if source_subgraph is not None:
            attrs["ltail"] = source_subgraph.graphviz_cluster_name
        if target_subgraph is not None:
            attrs["lhead"] = target_subgraph.graphviz_cluster_name

        dot.edge(source, target, **attrs)

    return dot


def render_flowchart_image(code: str) -> Optional[bytes]:
    """Render a Mermaid flowchart/graph as a PNG, or return None if it
    doesn't parse (unsupported syntax) or Graphviz fails to render it -
    either way the caller falls through to the existing code-block
    fallback, so this never needs to raise."""
    if graphviz is None:
        return None
    try:
        parsed = _parse_flowchart(code)
        if parsed is None:
            return None
        rankdir, root, labels, shapes, edges = parsed
        dot = _build_graphviz(rankdir, root, labels, shapes, edges)
        return dot.pipe(format="png")
    except Exception:
        LOG.warning("mermaid_graph: rendering failed, falling back to the code block", exc_info=True)
        return None
