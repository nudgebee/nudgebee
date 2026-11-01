// Kubernetes repeats an event under this prefix once it has seen the same message several times,
// and cuts the repeated message short with an ellipsis.
const COMBINED_PREFIX = /^\(combined from similar events\):\s*/;
const CUT_SHORT = /(…|\.\.\.)$/;
// A cut line is only matched against a longer one when this much of it survives; a few words
// followed by an ellipsis ("Waiting...") is a sentence of its own, not the start of another.
const MIN_CUT_LENGTH = 40;

/**
 * The lines for the box at the top of the investigation, each said once. The box collects every
 * critical line from every evidence block, so the same message arrived two or three times: from
 * two blocks, or once plain and once under Kubernetes' "(combined from similar events):" prefix.
 * A line that was cut short counts as a repeat of the line it is the start of, and the full line is
 * the one shown. Otherwise the first wording seen is kept, in the order first seen.
 *
 * @param {string[]} messages
 */
export const uniqueInsightLines = (messages) => {
  const kept = [];
  (Array.isArray(messages) ? messages : []).forEach((message) => {
    if (typeof message !== 'string' || !message.trim()) return;
    const stripped = message.replace(COMBINED_PREFIX, '').trim();
    const cutShort = CUT_SHORT.test(stripped) && stripped.replace(CUT_SHORT, '').trim().length >= MIN_CUT_LENGTH;
    const text = cutShort ? stripped.replace(CUT_SHORT, '').trim() : stripped;
    if (kept.some((line) => line.text === text || (cutShort && line.text.startsWith(text)))) return;
    // The full line arriving after its cut copy takes the copy's place.
    const cutCopy = cutShort ? undefined : kept.find((line) => line.cutShort && text.startsWith(line.text));
    if (cutCopy) {
      Object.assign(cutCopy, { message, text, cutShort: false });
      return;
    }
    kept.push({ message, text, cutShort });
  });
  return kept.map((line) => line.message);
};
