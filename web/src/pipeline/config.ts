// Pipeline prototype — config-driven stage machine. A pipeline (roles,
// stages, gate artifacts, task board, setup checklist) is fully described by
// a PipelineConfig: drop a pipeline.config.json next to the SPA to override
// the embedded demo.

export type GateVerdict = "pass" | "reject";

export type PipelineGate = {
    artifact: string;
    verdict: GateVerdict;
    human?: boolean;
};

export type PipelineStage = {
    id: string;
    name: string;
    role: string;
    docs: string[];
    gate?: PipelineGate;
    desc?: string;
    entry?: string;
    exit?: string;
};

export type SetupItem = {
    id: string;
    name: string;
    desc?: string;
    how?: string[];
    refs?: string[];
    status: "pending" | "done";
};

export type DocState = "pending" | "review" | "pass" | "reject";

export type PipelineTaskState = "running" | "gated" | "approval" | "done" | "rejected";

export type PipelineTask = {
    id: string;
    title: string;
    stageId: string;
    worker: string;
    state: PipelineTaskState;
    detail?: string;
    elapsed?: string;
    docs: Record<string, DocState>;
    quote?: { amount: string; margin?: string };
};

export type PipelineConfig = {
    name: string;
    setup: SetupItem[];
    stages: PipelineStage[];
    tasks: PipelineTask[];
};

export const demoConfig: PipelineConfig = {
    name: "biz-delivery",
    setup: [
        {
            id: "P1", name: "需求源配置", status: "pending",
            desc: "需求从哪来、怎么过滤。sources: 列表声明启用哪些源（邮箱 / 外部平台 / 客户上传文档 / 手动录入）及各自过滤规则；新源 = 加一条配置 + 一个 adapter。",
            how: [
                "编辑 ~/fusion/business/req-policy.md：sources: 逐源启用 + 过滤规则（邮箱白名单 / 外部平台技能标签 / 上传文档优先级）",
                "邮箱源走 fusion-channel email_imap / gmail_api / graph_mail，凭证进 keychain + AESGCM",
                "所有源汇入统一队列 POST /api/inbox/lead + source 标记，req 队列不关心来源",
            ],
            refs: ["req-policy.md", "channel-bridge 配置"],
        },
        {
            id: "P2", name: "工程脚手架", status: "pending",
            desc: "工程先搭好：~/fusion/business/ 目录规范、ai-XXXX 项目模板（立项时自动套用）、docs/ 契约骨架、dahai80-business 私有仓创建约定。",
            how: [
                "建目录规范：ai-XXXX/{docs,src,deploy}，deploy 全 docker 化（Dockerfile + compose）",
                "准备 ai-XXXX 模板仓（含 docs/ 契约空文件与说明），立项时 req 复制生成",
                "确认 gh 对 dahai80-business 的建仓/推送权限（deploy key 或 gh auth）",
            ],
            refs: ["ai-XXXX-template/", "dahai80-business 权限"],
        },
        {
            id: "P3", name: "团队绑定", status: "pending",
            desc: "五角色 worker 绑定：agent CLI（fusion-code headless）、provider（privacy: local-only 走 fusion-mlx，或显式授权云模型）、work_dir/repo、model 路由（rnd 大模型，valid 异家族小模型防互放过）。",
            how: [
                "console Agents > Workers 建 req / prd / rnd / valid / launch 五 worker",
                "rnd → fusion-code + 大模型（Qwen3.8-27B 或 8B+DSpine）；valid → 异家族 4B；其余聚合 4-8B 实例",
                "闭源仓 worker 勾 privacy: local-only（上云需显式授权）",
            ],
            refs: ["console Agents 页", "fusion-mlx#1743 映射"],
        },
        {
            id: "P4", name: "外发通道", status: "pending",
            desc: "报价回程通道：外部平台 OAuth（投标 API）、SMTP / gmail draft_mode（邮件回信）。均过审批卡，不批不发（fail-closed）。",
            how: [
                "注册外部接单平台 API 应用，OAuth 凭证入 keychain",
                "邮箱外发凭证配置（SMTP 或 gmail OAuth）",
                "审批卡接线 approvals / create_external（M2）",
            ],
            refs: ["外部平台 API keys", "fusion-channel outbox"],
        },
    ],
    stages: [
        {
            id: "S0", name: "需求筛选", role: "req", docs: ["req-draft.md"],
            gate: { artifact: "review-s0.md", verdict: "pass", human: true },
            desc: "已配置的源（P1）把需求送进统一队列；req 按 req-policy 过滤：技术栈匹配、预算下限、排期可行性，给出接/不接结论与理由。",
            entry: "任一已启用源出现新需求",
            exit: "用户人工确认筛选结论（人工闸门）：值得投 → 进 S1 评估报价；不接 → 归档 docs/rejected/",
        },
        {
            id: "S1", name: "评估/报价", role: "req", docs: ["work-estimate.md", "quote-ai-XXXX.md"],
            gate: { artifact: "quote-approval.md", verdict: "pass", human: true },
            desc: "干活之前先报价：req 粗估工作量 + 费率模板算价（参考 fusion-code #358 cost-per-task 遥测实证），生成报价单。用户批准后经对应源回程外发投标，等待中标。",
            entry: "S0 筛选通过（review-s0.md verdict=pass）",
            exit: "用户批准报价（人工闸门，fail-closed）→ 外发投标 → 中标确认 → 进 S2；未中标 → 归档留痕",
        },
        {
            id: "S2", name: "立项/PRD", role: "prd", docs: ["ai-XXXX-req.md", "prd.md", "archi-plan.md", "work-plan.md"],
            gate: { artifact: "review-prd.md", verdict: "pass" },
            desc: "中标后正式立项：套用 P2 脚手架建 ai-XXXX 仓（dahai80-business，闭源），req.md 沉淀需求；prd 输出 PRD、技术架构（必须逐条标注复用哪个 fusion-*，禁止重复造轮子）、详细工作计划（此时才能精确，报价阶段只做粗估）。",
            entry: "S1 中标确认",
            exit: "valid review 四份文档：PRD 可实现、archi-plan 复用映射完整、work-plan 拆解合理（与报价粗估偏差超阈值要回写复盘）",
        },
        {
            id: "S3", name: "开发", role: "rnd", docs: ["transcripts/"],
            gate: { artifact: "review-rnd.md", verdict: "pass" },
            desc: "rnd 按 work-plan 逐条交付，fusion-code headless 执行，per-task transcript 归档到 docs/transcripts/（artifact 端点聊天可回看）。一 issue 一 commit/PR。",
            entry: "S2 闸门通过（review-prd.md verdict=pass）",
            exit: "valid review 代码与 PR：符合 PRD、复用约束被遵守、publish audit 通过（commit+push+PR）",
        },
        {
            id: "S4", name: "验收测试", role: "launch", docs: ["acceptance-report.md"],
            gate: { artifact: "review-accept.md", verdict: "pass" },
            desc: "launch 用 docker 化部署（Dockerfile + compose），经 fusion-executor 执行 build/run，验收测试跑在 fusion-harness 沙箱，产出验收测试报告。",
            entry: "S3 闸门通过（review-rnd.md verdict=pass）",
            exit: "valid review acceptance-report.md：验收用例全过、部署可复现",
        },
        {
            id: "S5", name: "交付/归档", role: "req", docs: ["delivery-package.md"],
            gate: { artifact: "delivery-confirm.md", verdict: "pass", human: true },
            desc: "按源回程交付（平台交付 / 邮件回执 / 直接交付客户），归档全过程文档与成本（tokens/工时），回写报价偏差复盘。",
            entry: "S4 闸门通过（review-accept.md verdict=pass）",
            exit: "客户确认收货（人工闸门）→ 项目归档 → 周报/复盘",
        },
    ],
    tasks: [
        {
            id: "ai-1024", title: "电商 ERP 对接", stageId: "S3", worker: "rnd", state: "running", elapsed: "42m",
            docs: { "ai-XXXX-req.md": "pass", "prd.md": "pass", "archi-plan.md": "pass", "work-plan.md": "pass", "review-prd.md": "pass", "review-rnd.md": "review", "transcripts/": "pending" },
        },
        {
            id: "ai-1023", title: "会员小程序", stageId: "S1", worker: "req", state: "approval",
            detail: "报价待人工批准，批准后外发投标",
            docs: { "req-draft.md": "pass", "work-estimate.md": "pass", "quote-ai-XXXX.md": "review" },
            quote: { amount: "$2,400", margin: "38%" },
        },
        {
            id: "ai-1021", title: "品牌官网", stageId: "S4", worker: "launch", state: "running", elapsed: "62m",
            docs: { "ai-XXXX-req.md": "pass", "prd.md": "pass", "archi-plan.md": "pass", "work-plan.md": "pass", "review-prd.md": "pass", "review-rnd.md": "pass", "acceptance-report.md": "review" },
        },
        {
            id: "ai-1020", title: "数据爬虫", stageId: "S0", worker: "req", state: "rejected",
            detail: "rejected · 预算不足（req-policy.md 下限）",
            docs: { "req-draft.md": "reject" },
        },
    ],
};

export async function loadPipelineConfig(): Promise<{ config: PipelineConfig; source: "file" | "demo" }> {
    try {
        const res = await fetch("/pipeline.config.json", { headers: { Accept: "application/json" } });
        if (res.ok) {
            const cfg = await res.json();
            if (cfg && Array.isArray(cfg.stages) && Array.isArray(cfg.tasks)) {
                return { config: cfg as PipelineConfig, source: "file" };
            }
        }
    } catch {
        // fall through to the embedded demo config
    }
    return { config: demoConfig, source: "demo" };
}
