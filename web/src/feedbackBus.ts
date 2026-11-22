// Feedback bus: decouples deep stream loops (chat/agent) from the Shell-level
// toast host via window CustomEvents. Throttle: one prompt per error
// signature per 24h so a crashlooping task does not spam the user.
const THROTTLE_KEY = "fc-feedback-seen";
const THROTTLE_MS = 24 * 60 * 60 * 1000;

function seen(): Record<string, number> {
    try {
        return JSON.parse(localStorage.getItem(THROTTLE_KEY) ?? "{}") as Record<string, number>;
    } catch {
        return {};
    }
}

function markSeen(sig: string) {
    const now = Date.now();
    const m: Record<string, number> = {};
    for (const [k, v] of Object.entries(seen())) {
        if (now - v < THROTTLE_MS) m[k] = v;
    }
    m[sig] = now;
    try {
        localStorage.setItem(THROTTLE_KEY, JSON.stringify(m));
    } catch {
        /* storage full — throttle simply stops persisting */
    }
}

// Error signature: first 120 chars normalize digits/ids away, so the same
// failure with a different run number still dedupes.
export function errorSignature(text: string): string {
    return text.slice(0, 120).replace(/\d+/g, "#").trim();
}

// notifyFeedbackError fires the error-triggered toast (trigger path 2).
// Skips network-down noise: a dropped Wi-Fi is not a console bug.
export function notifyFeedbackError(message: string) {
    const text = (message ?? "").trim();
    if (!text || text.includes("Failed to fetch") || text.includes("network")) return;
    const sig = errorSignature(text);
    const last = seen()[sig];
    if (last && Date.now() - last < THROTTLE_MS) return;
    markSeen(sig);
    window.dispatchEvent(new CustomEvent("fc:feedback-error", { detail: { text } }));
}

// notifyFeedbackSuggestion fires after the brain's suggest_feedback tool ran
// (trigger path 3); the toast host offers the fetched draft on the page.
export function notifyFeedbackSuggestion(draft: { title: string; steps: string; expect: string; actual: string; extra: string; component: string }) {
    window.dispatchEvent(new CustomEvent("fc:feedback-suggest", { detail: { draft } }));
}
