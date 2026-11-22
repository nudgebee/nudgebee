// Widths of the left navigation columns, in px. Shared because the Nubi page
// positions its fixed header and composer past both of them.
export const MAIN_RAIL_WIDTH = 76;
export const NUBI_SUBNAV_WIDTH = 68;

// Rendered width of the open Chat History panel (ConversationListV2's
// `width: isConversationListVisible ? ds.space.mul(1, 75) : 0`, i.e.
// --ds-space-1 * 75 = 300px — its own `maxWidth` of 350px never binds).
// The panel is `position: absolute`, so it doesn't push the chat content —
// the content's own translateX shift on open has to match this number, or
// the panel overlaps whatever it's short by.
export const CONVERSATION_LIST_WIDTH_PX = 300;
