import { useCallback, useEffect, useState } from "react";
import { Sparkles, Upload } from "lucide-react";
import { api, type SkillEntry } from "./api";
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

// parseSkillFile splits a SKILL.md (or draft) into frontmatter fields + body.
// LLM drafts sometimes come wrapped in one markdown code fence — strip a
// single wrapping fence only; fences inside the body are preserved.
export function parseSkillFile(content: string): { name: string; description: string; body: string } {
    let text = content.trim();
    const fence = text.match(/^```(?:markdown)?\n([\s\S]*)\n```$/);
    if (fence) text = fence[1].trim();
    if (!text.startsWith("---")) return { name: "", description: "", body: text };
    const end = text.indexOf("\n---", 3);
    if (end < 0) return { name: "", description: "", body: text };
    const fm = text.slice(3, end);
    let name = "", description = "";
    for (const line of fm.split("\n")) {
        const t = line.trim();
        if (t.startsWith("name:")) name = t.slice(5).trim();
        if (t.startsWith("description:")) description = t.slice(12).trim();
    }
    return { name, description, body: text.slice(end + 4).trim() };
}

// SkillsPanel manages the SKILL.md directories headless workers load:
// list/delete installed skills, edit or create them (optionally AI-drafted
// from a hint or pasted reference text), and import vetted industry skills
// from GitHub.
export function SkillsPanel() {
    const [skills, setSkills] = useState<SkillEntry[]>([]);
    const [msg, setMsg] = useState("");
    const [err, setErr] = useState("");
    const [busy, setBusy] = useState(false);
    const [editing, setEditing] = useState(false);
    const [name, setName] = useState("");
    const [description, setDescription] = useState("");
    const [body, setBody] = useState("");
    const [scope, setScope] = useState<"global" | "project">("global");
    const [repo, setRepo] = useState("");
    const [draftHint, setDraftHint] = useState("");
    const [draftText, setDraftText] = useState("");
    const [importSource, setImportSource] = useState("");

    const load = useCallback(() => {
        api.skills(repo || undefined).then((r) => setSkills(r.skills ?? [])).catch((e) => setErr((e as Error).message));
    }, [repo]);
    useEffect(() => { load(); }, [load]);

    const openEditor = (name = "", description = "", body = "") => {
        setEditing(true);
        setName(name);
        setDescription(description);
        setBody(body);
        setMsg(""); setErr("");
    };

    const save = async () => {
        setBusy(true); setMsg(""); setErr("");
        try {
            const r = await api.saveSkill({ name, description, body, scope, repo });
            setMsg(`skill "${name}" saved to ${r.path}`);
            setEditing(false);
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
            const p = parseSkillFile(r.content);
            openEditor(p.name, p.description, p.body);
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
            const p = parseSkillFile(r.content);
            openEditor(p.name || source.split("/").pop() || "", p.description, p.body);
            setMsg(`imported from ${source} — review before saving`);
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

            <div className="space-y-2">
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

            {!editing && (
                <div className="flex flex-wrap gap-2">
                    <Button variant="outline" onClick={() => openEditor()}>New skill</Button>
                </div>
            )}

            {editing && (
                <Card className="space-y-3 p-4">
                    <h3 className="font-medium">{name ? `Edit skill: ${name}` : "New skill"}</h3>
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
                    <div className="flex gap-2">
                        <Button onClick={save} disabled={busy || !name || !body}>Save skill</Button>
                        <Button variant="ghost" onClick={() => setEditing(false)}>Cancel</Button>
                    </div>
                </Card>
            )}

            <Card className="space-y-3 p-4">
                <h3 className="font-medium flex items-center gap-2"><Sparkles size={16} /> AI draft</h3>
                <p className="text-xs text-zinc-500">Describe the skill (or paste reference material — session logs, manuals, wiki) and the brain drafts a SKILL.md. Review before saving.</p>
                <Input placeholder="what should this skill do?" value={draftHint} onChange={(e) => setDraftHint(e.target.value)} />
                <Textarea className="min-h-20 text-xs" placeholder="optional: paste reference text to distill" value={draftText} onChange={(e) => setDraftText(e.target.value)} />
                <Button onClick={draft} disabled={busy || (!draftHint && !draftText)}>Draft with AI</Button>
            </Card>

            <Card className="space-y-3 p-4">
                <h3 className="font-medium flex items-center gap-2"><Upload size={16} /> Import from GitHub</h3>
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
            </Card>

            {msg && <p className="text-sm text-indigo-500">{msg}</p>}
            {err && <p className="text-sm text-red-500">{err}</p>}
        </div>
    );

    async function editSkill(s: SkillEntry) {
        setMsg(""); setErr("");
        try {
            const r = await api.readSkill(s.path, s.scope === "project" ? repo || undefined : undefined);
            const p = parseSkillFile(r.content);
            openEditor(p.name || s.name, p.description || s.description, p.body);
        } catch (ex) {
            setErr((ex as Error).message);
        }
    }
}
