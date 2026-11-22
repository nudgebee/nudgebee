// The platform's own turns in a conversation (a planning turn, a follow-up check, the investigation summary)
// carry message_config.system_note: the one line shown for that turn instead of a person's message.
// message_config arrives as a JSON string or an already-parsed object.
export function systemNoteOf(messageConfig) {
  let config = messageConfig;
  if (typeof config === 'string') {
    try {
      config = JSON.parse(config);
    } catch {
      return '';
    }
  }
  const note = config && typeof config === 'object' ? config.system_note : '';
  return typeof note === 'string' ? note.trim() : '';
}

// "16:04" in the viewer's time zone, or '' for a missing or unreadable time.
export function noteTime(value) {
  if (!value) return '';
  const at = new Date(value);
  if (Number.isNaN(at.getTime())) return '';
  return at.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', hour12: false });
}
