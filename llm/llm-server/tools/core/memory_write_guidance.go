package core

// MemoryWriteShapes is the one description of what memory_write saves and
// which type each shape takes. The planner nudge (agents) and the tool's own
// description (ee/memory) both embed it, so the trigger the planner reads and
// the contract the tool list shows can't drift apart. It lives here because
// this package is OSS and imported by both.
const MemoryWriteShapes = "Save in one of these four shapes:\n" +
	"- Imperative: 'remember', 'note', 'save this', 'don't forget', 'keep this in mind', 'store'\n" +
	"- Declarative preference: 'I prefer X', 'I'd rather see X', 'X works better for me', 'I use X', 'my default X is Y', 'always do X', 'never do Y'\n" +
	"- Declarative fact: 'our X is Y', 'X is at Y', 'X runs on Y', 'X goes through Y'\n" +
	"- Declarative playbook / flow: 'for X, my flow is: A → B → C', 'when X happens I do Y', 'here's how I investigate X'\n\n" +
	"The three declarative shapes need no 'remember' — a standalone statement in one of them is itself the save. If your reply would say 'noted', 'got it', 'saved' or 'I'll use X from now on', call memory_write in the same turn first.\n\n" +
	"DO NOT save pass-through context during an unrelated investigation. If the user drops a fact while asking about something else ('the OOM started at 2pm — by the way, checkout runs on node pool B — can you check limits?'), answer the question; do not save 'checkout runs on node pool B'. Save only when the durable statement is the point of the turn, or when the user explicitly asked you to remember.\n\n" +
	"Pick the type from the shape:\n" +
	"- type=preference — how this user wants you to answer or work (log source, default namespace, output format, units, the region they work in). `key` is a short snake_case identifier; `value` is what they said.\n" +
	"- type=fact — a truth about the system that holds no matter who asks (which component, version or path it uses, where a service runs, what it depends on, who owns it). `subject` names the resource; `body` is the assertion. 'Our service mesh is Linkerd, not Istio' is a fact even though it names a choice.\n" +
	"- type=playbook — an ordered sequence for handling a specific problem. `problem` names the trigger; `steps` is the ordered list.\n"
