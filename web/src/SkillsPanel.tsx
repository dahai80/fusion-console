import { useCallback, useEffect, useState } from "react";
import { Bot, FolderCheck, Library, PenLine, ShieldAlert, Sparkles, Upload } from "lucide-react";
import { api, type AgentSkills, type CatalogSkill, type SkillDoc, type SkillEntry } from "./api";
import { Badge, Button, Card, Input, Select, Textarea } from "./components/ui";

// Review notes shown above the editor for imported or AI-drafted skills:
// where the content came from and what the server-side audit flagged.
type SkillReview = { source?: string; warnings: string[]; missing: string[] };

type SkillTab = "installed" | "catalog" | "defaults" | "new" | "draft" | "import";

const TABS: { id: SkillTab; label: string; icon: typeof FolderCheck }[] = [
    { id: "installed", label: "Installed", icon: FolderCheck },
    { id: "catalog", label: "Catalog", icon: Library },
    { id: "defaults", label: "Agent defaults", icon: Bot },
    { id: "new", label: "New Skill", icon: PenLine },
    { id: "draft", label: "AI Draft", icon: Sparkles },
    { id: "import", label: "Import from GitHub", icon: Upload },
];

// SkillsPanel manages the SKILL.md directories headless workers load, for
// the agent picked at the top (each agent CLI reads its own directories):
// list/delete installed skills, install from the bundled catalog, apply the
// per-agent default profile, edit or create skills (optionally AI-drafted),
// and import a SKILL.md from GitHub.
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
    const [catalog, setCatalog] = useState<CatalogSkill[]>([]);
    const [agent, setAgent] = useState("claude");
    const [agents, setAgents] = useState<AgentSkills[]>([]);
    const layout = agents.find((a) => a.agent === agent);

    const loadAgents = useCallback(() => {
        api.skillAgents().then((r) => setAgents(r.agents ?? [])).catch((e) => setErr((e as Error).message));
    }, []);
    useEffect(() => { loadAgents(); }, [loadAgents]);

    const load = useCallback(() => {
        api.skills(repo || undefined, agent).then((r) => setSkills(r.skills ?? [])).catch((e) => setErr((e as Error).message));
    }, [repo, agent]);
    useEffect(() => { load(); }, [load]);

    const projectRepo = scope === "project" && repo.trim() ? repo.trim() : undefined;
    const loadCatalog = useCallback(() => {
        api.skillsCatalog(projectRepo, agent).then((r) => setCatalog(r.skills ?? [])).catch((e) => setErr((e as Error).message));
    }, [projectRepo, agent]);
    useEffect(() => { if (tab === "catalog") loadCatalog(); }, [tab, loadCatalog]);

    // installFromCatalog copies the whole vendored skill directory. A skill
    // that is already present is only replaced after explicit confirmation.
    const installFromCatalog = async (c: CatalogSkill) => {
        const status = scope === "project" ? c.project : c.global;
        if (scope === "project" && !projectRepo) { setErr("project scope needs an absolute repo path"); return; }
        if (status && !window.confirm(status === "modified"
            ? `"${c.name}" is installed with local changes. Replace it with the catalog version? Local edits will be lost.`
            : `Reinstall "${c.name}" from the catalog?`)) return;
        setBusy(true); setMsg(""); setErr("");
        try {
            const r = await api.installCatalogSkill({ name: c.name, scope, repo: projectRepo, agent, overwrite: !!status });
            setMsg(`skill "${c.name}" installed for ${agent} to ${r.path}`);
            loadCatalog();
            loadAgents();
            load();
        } catch (ex) {
            setErr((ex as Error).message);
        } finally {
            setBusy(false);
        }
    };

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
            const r = await api.saveSkill({ name, description, extra, body, scope, repo, agent });
            setMsg(`skill "${name}" saved for ${agent} to ${r.path}`);
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
            await api.deleteSkill(s.name, s.scope, s.scope === "project" ? repo || undefined : undefined, agent);
            setMsg(`skill "${s.name}" deleted`);
            load();
        } catch (ex) {
            setErr((ex as Error).message);
        }
    };

    // installDefaults copies the agent family's default profile skills that
    // are not installed yet. Existing (incl. locally modified) skills stay.
    const installDefaults = async (a: AgentSkills) => {
        const missing = a.skills.filter((sk) => !sk.status).map((sk) => sk.name);
        if (!missing.length) return;
        if (!window.confirm(`Install ${missing.length} default skill(s) for ${a.agent} into ${a.global}?\n\n${missing.join(", ")}`)) return;
        setBusy(true); setMsg(""); setErr("");
        try {
            const r = await api.installAgentDefaults({ agent: a.agent });
            const skipped = r.skipped.length ? ` · left alone: ${r.skipped.map((sk) => `${sk.name} (${sk.status})`).join(", ")}` : "";
            setMsg(`${a.agent}: installed ${r.installed.length} default skill(s) into ${r.dir}${skipped}`);
            loadAgents();
            load();
        } catch (ex) {
            setErr((ex as Error).message);
        } finally {
            setBusy(false);
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

    return (
        <div className="space-y-4">
            <div className="space-y-1">
                <div className="flex flex-wrap items-center gap-2 text-sm">
                    <span className="text-zinc-500">Agent</span>
                    <Select className="w-auto" value={agent} onChange={(e) => { setAgent(e.target.value); setMsg(""); setErr(""); }}>
                        {(agents.length ? agents.map((a) => a.agent) : ["claude"]).map((n) => <option key={n} value={n}>{n}</option>)}
                    </Select>
                </div>
                <p className="text-sm text-zinc-500">
                    Skills are <code>SKILL.md</code> directories agents load natively, and each agent CLI has its own
                    locations: <b>{agent}</b> loads global skills from <code>{layout?.global ?? "…"}</code> and project
                    skills from <code>&lt;repo&gt;/{layout?.project ?? "…"}</code>.
                    {layout?.also_reads?.length ? <> It also reads {layout.also_reads.map((d) => <code key={d} className="mr-1">{d}</code>)}.</> : null}
                </p>
            </div>

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
                    <p className="text-xs font-medium text-zinc-500">Installed skills ({skills.length})</p>
                    <div className="max-h-96 space-y-2 overflow-y-auto pr-1">
                        {skills.map((s) => (
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

            {tab === "catalog" && (
                <div className="space-y-3">
                    <p className="text-xs text-zinc-500">
                        Popular skills bundled with the console, copied verbatim from pinned upstream commits with their
                        license files. Install copies the whole skill directory (SKILL.md plus scripts and references).
                    </p>
                    <div className="flex flex-wrap items-center gap-2">
                        <Select className="w-auto" value={scope} onChange={(e) => setScope(e.target.value as "global" | "project")}>
                            <option value="global">install to global ({layout?.global ?? "…"})</option>
                            <option value="project">install to project (&lt;repo&gt;/{layout?.project ?? "…"})</option>
                        </Select>
                        {scope === "project" && (
                            <Input className="flex-1" placeholder="/absolute/path/to/repo" value={repo} onChange={(e) => setRepo(e.target.value)} />
                        )}
                    </div>
                    <div className="max-h-[32rem] space-y-4 overflow-y-auto pr-1">
                        {[...new Set(catalog.map((c) => c.category))].map((cat) => (
                            <div key={cat} className="space-y-2">
                                <p className="text-xs font-medium uppercase tracking-wide text-zinc-400">{cat}</p>
                                {catalog.filter((c) => c.category === cat).map((c) => {
                                    const status = scope === "project" ? c.project : c.global;
                                    return (
                                        <Card key={c.name} className="flex items-start justify-between gap-3 p-3 text-sm">
                                            <div className="min-w-0 space-y-1">
                                                <p className="flex flex-wrap items-center gap-2 font-medium">
                                                    {c.name}
                                                    <Badge>{c.license}</Badge>
                                                    {status === "installed" && <Badge tone="ok">installed</Badge>}
                                                    {status === "modified" && <Badge tone="warn">installed · modified</Badge>}
                                                </p>
                                                <p className="line-clamp-2 text-xs text-zinc-500">{c.description}</p>
                                                <p className="text-xs text-zinc-400">
                                                    <a className="hover:text-indigo-500 hover:underline" href={c.url} target="_blank" rel="noreferrer">
                                                        {c.source}@{c.commit.slice(0, 7)}
                                                    </a>
                                                    {" · "}{c.files} file{c.files === 1 ? "" : "s"} · {(c.bytes / 1024).toFixed(0)} KB
                                                </p>
                                            </div>
                                            <Button variant={status ? "ghost" : "default"} className="shrink-0" disabled={busy} onClick={() => installFromCatalog(c)}>
                                                {status ? "Reinstall" : "Install"}
                                            </Button>
                                        </Card>
                                    );
                                })}
                            </div>
                        ))}
                        {catalog.length === 0 && <p className="text-sm text-zinc-400">Loading catalog…</p>}
                    </div>
                </div>
            )}

            {tab === "defaults" && (
                <div className="space-y-3">
                    <p className="text-xs text-zinc-500">
                        Each agent family gets its own default skill set (<code>internal/skillcatalog/catalog.json</code> →
                        <code> profiles</code>). Nothing is installed automatically: <i>Install missing</i> copies only the
                        profile skills that are not there yet into that agent's global skills directory. Existing and
                        locally modified skills are left alone.
                    </p>
                    {agents.map((a) => {
                        const missing = a.skills.filter((sk) => !sk.status).length;
                        return (
                            <Card key={a.agent} className="space-y-2 p-3 text-sm">
                                <div className="flex flex-wrap items-center justify-between gap-2">
                                    <p className="flex flex-wrap items-center gap-2 font-medium">
                                        {a.agent} <Badge>{a.profile_title}</Badge>
                                        {!a.enabled && <Badge tone="warn">disabled</Badge>}
                                    </p>
                                    <Button variant={missing ? "default" : "ghost"} disabled={busy || missing === 0} onClick={() => installDefaults(a)}>
                                        {missing ? `Install missing (${missing})` : "All installed"}
                                    </Button>
                                </div>
                                <p className="break-all text-xs text-zinc-400">
                                    → <code>{a.global}</code>{" · "}
                                    {a.ref.startsWith("http")
                                        ? <a className="hover:text-indigo-500 hover:underline" href={a.ref} target="_blank" rel="noreferrer">path source</a>
                                        : <span>{a.ref}</span>}
                                </p>
                                {a.also_reads?.length ? (
                                    <p className="text-xs text-amber-600 dark:text-amber-400">
                                        {a.agent} also loads {a.also_reads.map((d) => <code key={d} className="mr-1">{d}</code>)} — skills installed for other agents are visible to it too.
                                    </p>
                                ) : null}
                                <div className="flex flex-wrap gap-1">
                                    {a.skills.map((sk) => (
                                        <Badge key={sk.name} tone={sk.status === "installed" ? "ok" : sk.status === "modified" ? "warn" : "neutral"}>
                                            {sk.name}{sk.status === "modified" ? " · modified" : ""}
                                        </Badge>
                                    ))}
                                </div>
                            </Card>
                        );
                    })}
                    {agents.length === 0 && <p className="text-sm text-zinc-400">Loading agents…</p>}
                </div>
            )}

            {tab === "new" && (
                <Card className="space-y-3 p-4">
                    <h3 className="font-medium">{name ? `Edit skill: ${name}` : "New skill"}</h3>
                    <p className="text-xs text-zinc-500">Saves for <b>{agent}</b> ({scope}) — switch the agent at the top to save a copy for another agent.</p>
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
                            <option value="global">global ({layout?.global ?? "…"})</option>
                            <option value="project">project (&lt;repo&gt;/{layout?.project ?? "…"})</option>
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
                    <h3 className="font-medium flex items-center gap-2"><Upload size={16} /> Import from GitHub</h3>
                    <div className="flex gap-2">
                        <Input className="flex-1" placeholder="owner/repo or owner/repo/path (SKILL.md is fetched)" value={importSource} onChange={(e) => setImportSource(e.target.value)} />
                        <Button onClick={() => importSkill(importSource)} disabled={busy || !importSource.trim()}>Fetch</Button>
                    </div>
                    <p className="text-xs text-zinc-500">
                        Fetches <code>SKILL.md</code> only — bundled scripts and references are not imported.
                        Popular skills ship complete in the <button className="text-indigo-500 hover:underline" onClick={() => setTab("catalog")}>Catalog</button> tab.
                    </p>
                </Card>
            )}
        </div>
    );

    async function editSkill(s: SkillEntry) {
        setMsg(""); setErr("");
        try {
            const r = await api.readSkill(s.path, s.scope === "project" ? repo || undefined : undefined, agent);
            openEditor({ ...r.doc, name: r.doc.name || s.name }, { scope: s.scope });
        } catch (ex) {
            setErr((ex as Error).message);
        }
    }
}
