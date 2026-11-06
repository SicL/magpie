// Projects & Tasks: an opt-in coordination view. DSH headless runs are explicit desktop actions.
magpie.plugin("orchestration", (ctx) => {
  let page = null;
  let projects = [];
  let tasks = [];
  let worktrees = [];
  let runs = [];
  let dshCapability = null;
  let openLog = "";
  let logText = "";
  let selected = "";
  let projectForm = false;
  let taskForm = false;
  let busy = false;

  function field(title, placeholder, value) {
    const box = el("label", "orch-field");
    box.append(el("span", "", t(title)));
    const input = el("input", "set-field");
    input.type = "text";
    input.placeholder = placeholder || "";
    input.value = value || "";
    input.setAttribute("aria-label", t(title));
    box.append(input);
    return { box, input };
  }

  function button(title, action, cls) {
    const b = el("button", cls || "text");
    b.type = "button";
    b.textContent = t(title);
    b.onclick = action;
    return b;
  }

  async function refresh() {
    if (!page) return;
    try {
      projects = await api("orchestration/projects");
      if (!projects.some((p) => p.id === selected)) selected = projects[0]?.id || "";
      tasks = selected ? await api("orchestration/tasks?project=" + encodeURIComponent(selected)) : [];
      worktrees = selected ? await api("orchestration/worktrees?project=" + encodeURIComponent(selected)) : [];
      runs = selected ? await api("orchestration/runs?project=" + encodeURIComponent(selected)) : [];
      if (!web && dshCapability===null) { try {dshCapability = await api("orchestration/agents/dsh");} catch {dshCapability={available:false};} }
      render();
    } catch (e) {
      page.replaceChildren(el("div", "empty-state", e.message));
    }
  }

  async function perform(work) {
    if (busy) return;
    busy = true;
    try {
      await work();
      projectForm = false;
      taskForm = false;
      await refresh();
    } catch (e) {
      status(e.message, "err");
      busy = false;
      return;
    }
    busy = false;
  }

  function projectEditor() {
    const form = el("form", "orch-form");
    const name = field("Project name", "TMS development");
    const repo = field("Git project folder", "D:\\projects\\repository");
    const branch = field("Base branch (optional)", "main");
    const wt = field("Worktree root (optional)", "D:\\agent-worktrees");
    form.append(name.box, repo.box, branch.box, wt.box);
    const actions = el("div", "orch-actions");
    const save = button("Register project", () => {});
    save.type = "submit";
    save.onclick = null;
    form.onsubmit = (e) => {
      e.preventDefault();
      perform(async () => {
        const p = await api("orchestration/projects", {
          name: name.input.value, repoPath: repo.input.value,
          baseBranch: branch.input.value, worktreeRoot: wt.input.value
        });
        selected = p.id;
        status(t("Project registered"), "ok");
      });
    };
    actions.append(save, button("Cancel", () => { projectForm = false; render(); }));
    form.append(actions);
    return form;
  }

  function taskEditor() {
    const form = el("form", "orch-form");
    const title = field("Task title", "Implement a new API");
    const details = field("Description and acceptance criteria", "Describe what must be verified");
    const agent = field("Assigned agent (optional)", "DeepSeek Harness");
    form.append(title.box, details.box, agent.box);
    const actions = el("div", "orch-actions");
    const save = button("Create task", () => {});
    save.type = "submit";
    save.onclick = null;
    form.onsubmit = (e) => {
      e.preventDefault();
      perform(async () => {
        await api("orchestration/tasks", {
          projectId: selected, title: title.input.value, details: details.input.value, agent: agent.input.value
        });
        status(t("Task created"), "ok");
      });
    };
    actions.append(save, button("Cancel", () => { taskForm = false; render(); }));
    form.append(actions);
    return form;
  }

  function stateLabel(value) {
    const labels = {
      pending: t("Task pending"), ready: t("Task ready"), running: t("Task running"),
      blocked: t("Task blocked"), failed: t("Task failed"),
      review: t("Task review"), done: t("Task complete")
    };
    return labels[value] || value;
  }
  function markLabel(value) {
    const labels = {
      ready: t("Mark ready"), running: t("Mark running"),
      blocked: t("Mark blocked"), failed: t("Mark failed"),
      review: t("Mark review"), done: t("Mark done")
    };
    return labels[value] || value;
  }
  const next = {
    pending: ["ready", "blocked"],
    ready: ["running", "blocked"],
    running: ["review", "failed", "blocked"],
    blocked: ["ready"],
    failed: ["ready"],
    review: ["done", "ready"],
    done: []
  };

  function render() {
    if (!page) return;
    page.replaceChildren();
    const head = el("div", "orch-head");
    const intro = el("div", "orch-intro");
    intro.append(el("b", "", t("Projects and tasks")),
      el("span", "", t(web ? "Browser mode is read-only." : "Assign tasks and start DeepSeek Harness only by explicit action.")));
    head.append(intro);
    if (!web) head.append(button("Add project", () => { projectForm = !projectForm; render(); }, "text"));
    head.append(button("Reload task status", () => refresh(), "text"));
    page.append(head);
    if (projectForm) page.append(projectEditor());

    const grid = el("div", "orch-grid");
    const left = el("section", "orch-section");
    left.append(el("h3", "", t("Projects")));
    if (!projects.length) left.append(el("p", "orch-empty", t("No projects registered")));
    for (const p of projects) {
      const item = button(p.name, async () => {
        selected = p.id;
        taskForm = false;
        await refresh();
      }, "orch-project");
      item.classList.toggle("on", p.id === selected);
      item.title = p.repoPath;
      item.append(el("small", "", p.repoPath));
      left.append(item);
    }
    grid.append(left);

    const right = el("section", "orch-section");
    const toolbar = el("div", "orch-toolbar");
    const active = projects.find((p) => p.id === selected);
    toolbar.append(el("h3", "", active ? active.name : t("Project task list")));
    if (active && !web) toolbar.append(button("Add task", () => { taskForm = !taskForm; render(); }));
    right.append(toolbar);
    if (active) {
      right.append(el("p", "orch-path", active.repoPath + " · " + active.baseBranch));
      if (!web && dshCapability && !dshCapability.available) right.append(el("p", "orch-empty", t("DeepSeek Harness CLI unavailable; tasks remain manual.")));
      if (!web && dshCapability?.available) right.append(el("p","orch-safety",t("Agent runs use your local account permissions; verify each task before starting.")));
      if (taskForm) right.append(taskEditor());
      if (!tasks.length) right.append(el("p", "orch-empty", t("No tasks in this project")));
      for (const q of tasks) {
        const item = el("div", "orch-task");
        const top = el("div", "orch-task-top");
        top.append(el("strong", "", q.title), el("span", "orch-state", t("Workflow state") + ": " + stateLabel(q.status)));
        item.append(top);
        if (q.details) item.append(el("p", "orch-detail", q.details));
        item.append(el("small", "", (q.agent || t("Unassigned")) + " · " + t("Task version") + " " + q.revision));
        const existing = worktrees.find((w) => w.path.endsWith("task-" + q.id.slice(0, 16)));
        const latestRun = runs.find((r) => r.taskId === q.id);
        if (existing) {
          item.append(el("small", "orch-worktree", existing.path + " · " + existing.branch));
        }
        if (latestRun) {
          const summary = el("div", "orch-run-summary");
          summary.append(el("span", "", t("Latest agent run") + ": " + latestRun.state));
          if(latestRun.sessionId) summary.append(el("span","",t("DSH session ID")+": "+latestRun.sessionId));
          if (latestRun.exitCode != null) summary.append(el("span", "", " · " + t("Agent exit code") + " " + latestRun.exitCode));
          if (latestRun.failure) summary.append(el("div", "orch-failure", latestRun.failure));
          item.append(summary);
          if (openLog === latestRun.id && !web) item.append(el("pre", "orch-log", logText));
        }
        const actions = el("div", "orch-actions");
        if (web) { right.append(item); continue; }
        const isDSH = ["deepseek harness", "dsh"].includes((q.agent || "").trim().toLowerCase());
        if (isDSH && existing && q.status === "ready" && dshCapability?.available) {
          actions.append(button("Start DeepSeek Harness", () => perform(async () => {
            await api("orchestration/tasks/" + encodeURIComponent(q.id) + "/run", {revision:q.revision});
            status(t("Agent process started"), "ok");
          })));
        }
        if (latestRun) {
          actions.append(button("View agent log", () => perform(async () => {
            const log=await api("orchestration/runs/" + encodeURIComponent(latestRun.id) + "/log");
            openLog=latestRun.id;
            logText=log.tail || t("Agent log has no output yet");
          })));
          if (["starting", "running"].includes(latestRun.state)) {
            actions.append(button("Stop owned agent run", () => perform(async () => {
              await api("orchestration/runs/" + encodeURIComponent(latestRun.id) + "/stop", {});
              status(t("Agent stop requested"), "ok");
            })));
          }
        }
        for (const statusName of next[q.status] || []) {
          if (isDSH && existing && statusName === "running") continue;
          actions.append(button(markLabel(statusName), () => perform(async () => {
            await api("orchestration/tasks/" + encodeURIComponent(q.id) + "/status", {
              revision: q.revision, status: statusName
            });
          })));
        }
        if (!existing && q.status !== "done") {
          actions.append(button("Create isolated Worktree", () => perform(async () => {
            await api("orchestration/tasks/" + encodeURIComponent(q.id) + "/worktree", {});
            status(t("Worktree created"), "ok");
          })));
        }
        if (["pending", "blocked", "failed", "done"].includes(q.status)) {
          actions.append(button("Remove task", () => perform(async () => {
            await api("orchestration/tasks/" + encodeURIComponent(q.id) + "/remove", {});
          }), "text"));
        }
        item.append(actions);
        right.append(item);
      }
      if (!tasks.length && !web) {
        right.append(button("Remove empty project", () => perform(async () => {
          await api("orchestration/projects/" + encodeURIComponent(active.id) + "/remove", {});
          selected = "";
        })));
      }
    } else {
      right.append(el("p", "orch-empty", t("Register a Git repository to start")));
    }
    grid.append(right);
    page.append(grid);
  }

  ctx.slots.add("page", {
    key: "orchestration", title: "Projects",
    mount(main) { page = main; },
    open: () => refresh(),
    relang: () => render(),
  });
});
