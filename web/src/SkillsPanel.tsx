import { useCallback, useEffect, useState } from "react";
import { FolderCheck, PenLine, ShieldAlert, Sparkles, Upload } from "lucide-react";
import { api, type SkillDoc, type SkillEntry } from "./api";
import { Badge, Button, Card, Input, Select, Textarea } from "./components/ui";

// Curated industry skills worth one-click importing. Sources are pinned to
// repos we have vetted; everything else can be typed into the import box.
const CURATED: { label: string; source: string }[] = [
    { label: "skill-creator (Anthropic)", source: "anthropics/skills/skills/skill-creator" },
    { label: "frontend-design (Anthropic)", source: "anthropics/skills/skills/frontend-design" },
    { label: "mcp-builder (Anthropic)", source: "anthropics/skills/skills/mcp-builder" },
    { label: "webapp-testing (Anthropic)", source: "anthropics/skills/skills/webapp-testing" },
    { label: "docx (Anthropic)", source: "anthropics/skills/skills/docx" },
    { label: "pdf (Anthropic)", source: "anthropics/skills/skills/pdf" },
    { label: "brainstorming (superpowers)", source: "obra/superpowers/skills/brainstorming" },
    { label: "systematic-debugging (superpowers)", source: "obra/superpowers/skills/systematic-debugging" },
    { label: "test-driven-development (superpowers)", source: "obra/superpowers/skills/test-driven-development" },
    { label: "writing-plans (superpowers)", source: "obra/superpowers/skills/writing-plans" },
];

// Review notes shown above the editor for imported or AI-drafted skills:
// where the content came from and what the server-side audit flagged.
type SkillReview = { source?: string; warnings: string[]; missing: string[] };

type SkillTab = "installed" | "new" | "draft" | "import";

const TABS: { id: SkillTab; label: string; icon: typeof FolderCheck }[] = [
    { id: "installed", label: "Installed", icon: FolderCheck },
    { id: "new", label: "New Skill", icon: PenLine },
    { id: "draft", label: "AI Draft", icon: Sparkles },
    { id: "import", label: "Import", icon: Upload },
];

// SkillsPanel manages the SKILL.md directories headless workers load:
// list/delete installed skills, edit or create them (optionally AI-drafted
// from a hint or pasted reference text), and import vetted industry skills
// from GitHub. Four tabs instead of one long scroll.
export function SkillsPanel() {
    const [tab, setTab] = useState<SkillTab>("installed");
    const [skills, setSkills] = useState<SkillEntry[]>([]);
    const [msg, setMsg] = useState("");
    const [err, setErr] = useState("");
    const [busy, setBusy] = useState(false);
    const [editing, setEditing] = useState(false);
    const [name, setName] = useState("");
    const [description, setDescription] = useState("");
    const [body, setBody] = useState("");
    const [extra, setExtra] = useState("");
    const [review, setReview] = useState<SkillReview | null>(null);
    const [scope, setScope] = useState<"global" | "project">("global");
    const [repo, setRepo] = useState("");
    const [draftHint, setDraftHint] = useState("");
    const [draftText, setDraftText] = useState("");
    const [importSource, setImportSource] = useState("");
const [importKind, setImportKind] = useState<"github" | "local">("github");
const [localPath, setLocalPath] = useState("");
const [skillQuery, setSkillQuery] = useState("");

    const load = useCallback(() => {
        api.skills(repo || undefined).then((r) => setSkills(r.skills ?? [])).catch((e) => setErr((e as Error).message));
    }, [repo]);
    useEffect(() => { load(); }, [load]);

    const openEditor = (doc?: Partial<SkillDoc>, opts: { scope?: "global" | "project"; review?: SkillReview } = {}) => {
        setEditing(true);
        setName(doc?.name ?? "");
        setDescription(doc?.description ?? "");
        setExtra(doc?.extra ?? "");
        setBody(doc?.body ?? "");
        // Keep the skill's own scope so saving an edit never writes a
        // project skill into the global directory.
        if (opts.scope) setScope(opts.scope);
        setReview(opts.review ?? null);
        setMsg(""); setErr("");
        setTab("new");
    };

    const save = async () => {
        setBusy(true); setMsg(""); setErr("");
        try {
            const r = await api.saveSkill({ name, description, extra, body, scope, repo });
            setMsg(`skill "${name}" saved to ${r.path}`);
            setEditing(false);
            setReview(null);
            setTab("installed");
            load();
        } catch (ex) {
            setErr((ex as Error).message);
        } finally {
            setBusy(false);
        }
    };

    const remove = async (s: SkillEntry) => {
        setMsg(""); setErr("");
        try {
            await api.deleteSkill(s.name, s.scope, s.scope === "project" ? repo || undefined : undefined);
            setMsg(`skill "${s.name}" deleted`);
            load();
        } catch (ex) {
            setErr((ex as Error).message);
        }
    };

    const draft = async () => {
        setBusy(true); setMsg(""); setErr("");
        try {
            const r = await api.draftSkill({ hint: draftHint, text: draftText });
            openEditor(r.doc, { review: { source: "AI draft", warnings: r.warnings ?? [], missing: [] } });
            setMsg("draft generated — review and edit before saving");
        } catch (ex) {
            setErr((ex as Error).message);
        } finally {
            setBusy(false);
        }
    };

    const importSkill = async (source: string) => {
        if (!source.trim()) return;
        setBusy(true); setMsg(""); setErr("");
        try {
            const r = await api.importSkill(source.trim());
            openEditor({ ...r.doc, name: r.doc.name || source.split("/").pop() || "" }, {
                review: { source: r.url, warnings: r.warnings ?? [], missing: r.missing_files ?? [] },
            });
            setMsg(`imported from ${source} — review before saving`);
        } catch (ex) {
            setErr((ex as Error).message);
        } finally {
            setBusy(false);
        }
    };

    const importLocal = async (path: string) => {
        if (!path.trim()) return;
        setBusy(true); setMsg(""); setErr("");
        try {
            const r = await api.localImportSkill(path.trim());
            openEditor({ ...r.doc, name: r.doc.name || path.trim().split("/").filter(Boolean).pop() || "" }, {
                review: { source: r.url, warnings: r.warnings ?? [], missing: r.missing_files ?? [] },
            });
            setMsg(`read from ${path.trim()} — review before saving`);
        } catch (ex) {
            setErr((ex as Error).message);
        } finally {
            setBusy(false);
        }
    };

    return (
        <div className="space-y-4">
            <p className="text-sm text-zinc-500">
                Skills are <code>SKILL.md</code> directories agents load natively — global ones from
                <code> ~/.claude/skills/</code>, project ones from <code>&lt;repo&gt;/.claude/skills/</code>.
                Scope only applies to the Claude agent.
            </p>

            <div className="flex flex-wrap gap-1 rounded-xl border border-zinc-200/80 bg-zinc-100/60 p-1 dark:border-zinc-800 dark:bg-zinc-900/60">
                {TABS.map(({ id, label, icon: Icon }) => (
                    <button key={id} onClick={() => { setTab(id); if (id !== "new") { setEditing(false); setReview(null); } }}
                        className={`flex items-center gap-1.5 rounded-lg px-3 py-1.5 text-sm transition-colors ${
                            tab === id
                                ? "bg-white text-indigo-600 shadow-sm dark:bg-zinc-800 dark:text-indigo-400"
                                : "text-zinc-500 hover:text-zinc-700 dark:hover:text-zinc-300"
                        }`}>
                        <Icon size={14} /> {label}
                    </button>
                ))}
            </div>

            {msg && <p className="text-sm text-indigo-500">{msg}</p>}
            {err && <p className="text-sm text-red-500">{err}</p>}

            {tab === "installed" && (
                <div className="space-y-2">
                    <div className="flex items-center justify-between gap-2">
                        <p className="text-xs font-medium text-zinc-500">Installed skills ({skills.length})</p>
                        <Input
                            value={skillQuery}
                            onChange={(e) => setSkillQuery(e.target.value)}
                            placeholder="Filter by name or description"
                            className="h-8 w-56 text-xs"
                        />
                    </div>
                    <div className="max-h-96 space-y-2 overflow-y-auto pr-1">
                        {skills.filter((s) => {
                            const q = skillQuery.trim().toLowerCase();
                            if (!q) return true;
                            return s.name.toLowerCase().includes(q) || (s.description || "").toLowerCase().includes(q) || s.path.toLowerCase().includes(q);
                        }).map((s) => (
                            <Card key={s.path} className="flex items-center justify-between p-3 text-sm">
                                <div className="min-w-0">
                                    <p className="font-medium flex items-center gap-2">
                                        {s.name} <Badge>{s.scope}</Badge>
                                    </p>
                                    <p className="truncate text-xs text-zinc-500">{s.description || s.path}</p>
                                </div>
                                <div className="flex shrink-0 items-center gap-1">
                                    <Button variant="ghost" onClick={() => editSkill(s)}>edit</Button>
                                    <button onClick={() => remove(s)} title={`Delete skill ${s.name}`}
                                        className="rounded-lg px-2 py-1 text-xs text-zinc-400 hover:bg-zinc-100 hover:text-red-500 dark:hover:bg-zinc-800">
                                        delete
                                    </button>
                                </div>
                            </Card>
                        ))}
                        {skills.length === 0 && <p className="text-sm text-zinc-400">No skills installed yet.</p>}
                    </div>
                </div>
            )}

            {tab === "new" && (
                <Card className="space-y-3 p-4">
                    <h3 className="font-medium">{name ? `Edit skill: ${name}` : "New skill"}</h3>
                    {review && (review.source || review.warnings.length > 0 || review.missing.length > 0) && (
                        <div className="space-y-1 rounded-lg border border-amber-300/70 bg-amber-50 p-3 text-xs text-amber-800 dark:border-amber-700/60 dark:bg-amber-950/40 dark:text-amber-300">
                            {review.source && <p className="break-all">source: <code>{review.source}</code></p>}
                            {review.warnings.length > 0 && (
                                <>
                                    <p className="flex items-center gap-1 font-medium"><ShieldAlert size={14} /> Review before saving — skills run with the agent's permissions:</p>
                                    <ul className="list-disc pl-5">{review.warnings.map((w) => <li key={w}>{w}</li>)}</ul>
                                </>
                            )}
                            {review.missing.length > 0 && (
                                <p>Only SKILL.md was imported. It references files that were not fetched: {review.missing.map((f) => <code key={f} className="mr-1">{f}</code>)}</p>
                            )}
                            {review.warnings.length === 0 && review.missing.length === 0 && <p>Automatic checks found nothing — still read it before saving.</p>}
                        </div>
                    )}
                    <div className="grid grid-cols-2 gap-3">
                        <Input placeholder="name (lowercase-dash)" value={name} onChange={(e) => setName(e.target.value)} />
                        <Input placeholder="description — when should the agent use it" value={description} onChange={(e) => setDescription(e.target.value)} />
                        <Select value={scope} onChange={(e) => setScope(e.target.value as "global" | "project")}>
                            <option value="global">global (~/.claude/skills)</option>
                            <option value="project">project (&lt;repo&gt;/.claude/skills)</option>
                        </Select>
                        {scope === "project" && (
                            <Input placeholder="/absolute/path/to/repo" value={repo} onChange={(e) => setRepo(e.target.value)} />
                        )}
                    </div>
                    <Textarea className="min-h-48 font-mono text-xs" placeholder="Skill body — markdown instructions, steps, guardrails" value={body} onChange={(e) => setBody(e.target.value)} />
                    <details open={extra.trim() !== ""} className="text-xs">
                        <summary className="cursor-pointer text-zinc-500">Other frontmatter fields (allowed-tools, license, metadata …) — saved as-is</summary>
                        <Textarea className="mt-2 min-h-16 font-mono text-xs" placeholder={"allowed-tools: Bash, Read\nlicense: MIT"} value={extra} onChange={(e) => setExtra(e.target.value)} />
                    </details>
                    <div className="flex gap-2">
                        <Button onClick={save} disabled={busy || !name || !body}>Save skill</Button>
                        <Button variant="ghost" onClick={() => { setEditing(false); setReview(null); setTab("installed"); }}>Cancel</Button>
                    </div>
                </Card>
            )}

            {tab === "draft" && (
                <Card className="space-y-3 p-4">
                    <h3 className="font-medium flex items-center gap-2"><Sparkles size={16} /> AI draft</h3>
                    <p className="text-xs text-zinc-500">Describe the skill (or paste reference material — session logs, manuals, wiki) and the brain drafts a SKILL.md. Review before saving.</p>
                    <Input placeholder="what should this skill do?" value={draftHint} onChange={(e) => setDraftHint(e.target.value)} />
                    <Textarea className="min-h-20 text-xs" placeholder="optional: paste reference text to distill" value={draftText} onChange={(e) => setDraftText(e.target.value)} />
                    <Button onClick={draft} disabled={busy || (!draftHint && !draftText)}>Draft with AI</Button>
                </Card>
            )}

            {tab === "import" && (
                <Card className="space-y-3 p-4">
                    <h3 className="font-medium flex items-center gap-2"><Upload size={16} /> Import skill</h3>
                    <Select value={importKind} onChange={(e) => setImportKind(e.target.value as "github" | "local")} className="w-44">
                        <option value="github">GitHub repo</option>
                        <option value="local">Local directory</option>
                    </Select>
                    {importKind === "github" ? (
                        <>
                            <div className="flex gap-2">
                                <Input className="flex-1" placeholder="owner/repo or owner/repo/path (SKILL.md is fetched)" value={importSource} onChange={(e) => setImportSource(e.target.value)} />
                                <Button onClick={() => importSkill(importSource)} disabled={busy || !importSource.trim()}>Fetch</Button>
                            </div>
                            <div className="flex flex-wrap gap-1">
                                {CURATED.map((c) => (
                                    <button key={c.source} onClick={() => { setImportSource(c.source); importSkill(c.source); }} disabled={busy}
                                        className="rounded-lg border border-zinc-200/80 px-2 py-1 text-xs text-zinc-500 hover:border-indigo-400 hover:text-indigo-600 dark:border-zinc-800 dark:hover:text-indigo-400">
                                        {c.label}
                                    </button>
                                ))}
                            </div>
                        </>
                    ) : (
                        <>
                            <p className="text-xs text-zinc-500">Reads <code>SKILL.md</code> from an absolute directory on this host — review, then save to install.</p>
                            <div className="flex gap-2">
                                <Input className="flex-1" placeholder="/absolute/path/to/skill-directory" value={localPath} onChange={(e) => setLocalPath(e.target.value)} />
                                <Button onClick={() => importLocal(localPath)} disabled={busy || !localPath.trim()}>Read &amp; review</Button>
                            </div>
                        </>
                    )}
                </Card>
            )}
        </div>
    );

    async function editSkill(s: SkillEntry) {
        setMsg(""); setErr("");
        try {
            const r = await api.readSkill(s.path, s.scope === "project" ? repo || undefined : undefined);
            openEditor({ ...r.doc, name: r.doc.name || s.name }, { scope: s.scope });
        } catch (ex) {
            setErr((ex as Error).message);
        }
    }
}
