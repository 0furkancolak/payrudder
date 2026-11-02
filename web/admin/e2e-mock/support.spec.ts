import { expect, test } from "@playwright/test";
import { MockGateway, signInAsOperator } from "./gateway";
import { ACCOUNT, APP, productGateway, signInAsAda } from "./product";

// The help desk: the organisation's tickets (open one, answer on it, mark it solved, reopen it) and the operator's inbox (filter, answer publicly or leave an
// internal note, set status, priority and assignee). The gateway is mocked with one shared set of tickets, so what one side writes the other reads.

interface Msg {
  id: string;
  author_kind: "customer" | "operator";
  author_name: string;
  body: string;
  internal: boolean;
  create_time: string;
}
interface Ev {
  id: string;
  kind: "status" | "priority" | "assignee" | "tags";
  actor_kind: "customer" | "operator" | "system";
  actor_name: string;
  from_value: string;
  to_value: string;
  create_time: string;
}
interface Ticket {
  id: string;
  number: number;
  account_id: string;
  account_name: string;
  app_id: string;
  app_name: string;
  opened_by: string;
  opened_by_name: string;
  subject: string;
  category: string;
  priority: string;
  status: string;
  assignee: string;
  create_time: string;
  update_time: string;
  last_message_time: string;
  last_author: string;
  first_response_due: string;
  first_response_time: string | null;
  sla_breached: boolean;
  solve_time: string | null;
  message_count: number;
  unread: boolean;
  tags: string[];
  messages: Msg[];
  events: Ev[];
}

const NOW = "2026-10-28T09:00:00Z";
function ticket(over: Partial<Ticket> = {}): Ticket {
  return {
    id: "c0000000-0000-4000-8000-000000000001", number: 1042, account_id: ACCOUNT, account_name: "Ada Labs", app_id: APP, app_name: "Shop", opened_by: "u1", opened_by_name: "Ada",
    subject: "Paddle webhooks are late", category: "integration", priority: "high", status: "open", assignee: "", create_time: NOW, update_time: NOW, last_message_time: NOW,
    last_author: "customer", first_response_due: "2026-10-28T13:00:00Z", first_response_time: null, sla_breached: false, solve_time: null, message_count: 1, unread: false, tags: [], events: [],
    messages: [{ id: "m1", author_kind: "customer", author_name: "Ada", body: "Since this morning.", internal: false, create_time: NOW }],
    ...over,
  };
}

/** One set of tickets behind both sides. `internal` messages are in the operator's view only, and "unread" is the opener's: the operator never has it. */
function helpDesk(gw: MockGateway, tickets: Ticket[]) {
  const view = (t: Ticket, operator: boolean) => {
    const messages = t.messages.filter((m) => operator || !m.internal);
    const rest: Omit<Ticket, "messages" | "events"> & { messages?: Msg[]; events?: Ev[] } = { ...t };
    delete rest.messages;
    delete rest.events;
    return { ticket: { ...rest, tags: operator ? t.tags : [], unread: operator ? false : t.unread, message_count: messages.filter((m) => !m.internal).length }, messages, events: t.events };
  };
  const find = (path: string) => tickets.find((t) => path.includes(t.id));
  gw.on((c) => (c.method === "GET" && c.path === "/admin/v1/support/operators" ? { body: { operators: [{ email: "sam@example.com", name: "Sam" }, { email: "kim@example.com", name: "" }] } } : undefined));
  gw.on((c) => {
    if (c.path === "/app/v1/support/tickets" && c.method === "GET") return { body: { tickets: tickets.map((t) => view(t, false).ticket) } };
    if (c.path === "/app/v1/support/tickets" && c.method === "POST") {
      const b = c.body as { subject: string; category: string; priority?: string; body: string; app_id?: string };
      const t = ticket({ id: `c0000000-0000-4000-8000-00000000009${tickets.length}`, number: 1042 + tickets.length, subject: b.subject, category: b.category, priority: b.priority ?? "normal", app_id: b.app_id ?? "", app_name: b.app_id ? "Shop" : "",
        messages: [{ id: "n1", author_kind: "customer", author_name: "Ada", body: b.body, internal: false, create_time: NOW }] });
      tickets.unshift(t);
      return { status: 201, body: { ticket: view(t, false).ticket } };
    }
    if (c.path.startsWith("/app/v1/support/tickets/") && c.method === "GET") {
      const t = find(c.path);
      // Reading it clears the opener's "new" mark (the answer to this call already shows it cleared).
      if (t) t.unread = false;
      return t ? { body: view(t, false) } : { status: 404, body: { error: { code: "NOT_FOUND", key: "support.ticket.not_found", message: "no" } } };
    }
    if (c.path.endsWith("/replies") && c.path.startsWith("/app/v1/")) {
      const t = find(c.path)!;
      t.messages.push({ id: `m${t.messages.length + 1}`, author_kind: "customer", author_name: "Ada", body: String(c.body?.body), internal: false, create_time: NOW });
      t.status = "open";
      return { status: 201, body: { ticket: view(t, false).ticket } };
    }
    if (c.path.endsWith("/status") && c.path.startsWith("/app/v1/")) {
      const t = find(c.path)!;
      t.status = String(c.body?.status);
      return { body: { ticket: view(t, false).ticket } };
    }
    if (c.path === "/admin/v1/support/tickets" && c.method === "GET") {
      const status = c.query.get("status");
      const tag = c.query.get("tag");
      const list = tickets.filter((t) => (!status ? true : status === "active" ? ["open", "pending"].includes(t.status) : t.status === status) && (c.query.get("sla_breached") !== "true" || t.sla_breached) && (!tag || t.tags.includes(tag)));
      return { body: { tickets: list.map((t) => view(t, true).ticket) } };
    }
    if (c.path.startsWith("/admin/v1/support/tickets/") && c.method === "GET") return { body: view(find(c.path)!, true) };
    if (c.path.endsWith("/replies") && c.path.startsWith("/admin/v1/")) {
      const t = find(c.path)!;
      const internal = c.body?.internal === true;
      t.messages.push({ id: `m${t.messages.length + 1}`, author_kind: "operator", author_name: "user1@example.com", body: String(c.body?.body), internal, create_time: NOW });
      if (!internal) {
        t.status = t.status === "open" ? "pending" : t.status;
        t.first_response_time ??= NOW;
        t.last_author = "operator";
        t.unread = true;
      }
      return { status: 201, body: { ticket: view(t, true).ticket } };
    }
    if (c.path.startsWith("/admin/v1/support/tickets/") && c.method === "PATCH") {
      const t = find(c.path)!;
      const patch = Object.fromEntries(Object.entries(c.body ?? {}).filter(([, v]) => v !== undefined));
      if (Array.isArray(patch.tags) && patch.tags.join(",") !== t.tags.join(",")) {
        t.events.push({ id: `e${t.events.length + 1}`, kind: "tags", actor_kind: "operator", actor_name: "user1@example.com", from_value: t.tags.join(","), to_value: patch.tags.join(","), create_time: NOW });
      }
      for (const kind of ["status", "priority", "assignee"] as const) {
        const to = patch[kind];
        if (typeof to === "string" && to !== t[kind]) t.events.push({ id: `e${t.events.length + 1}`, kind, actor_kind: "operator", actor_name: "user1@example.com", from_value: t[kind], to_value: to, create_time: NOW });
      }
      if (patch.status && patch.status !== t.status) t.unread = true;
      Object.assign(t, patch);
      return { body: { ticket: view(t, true).ticket } };
    }
    return undefined;
  });
  return tickets;
}

test.describe("support: the organisation's side", () => {
  test("opens a ticket, reads the thread, answers, marks it solved and reopens it", async ({ page }) => {
    const gw = productGateway(new MockGateway()).gw;
    const tickets = helpDesk(gw, [ticket()]);
    await gw.install(page);
    await signInAsAda(page);
    await page.goto("/app/support");
    await expect(page.getByTestId("support-row-1042")).toContainText("Paddle webhooks are late");
    await expect(page.getByTestId("support-row-1042").getByTestId("support-status")).toHaveText("Waiting for us");

    await page.getByTestId("support-new").click();
    const sheet = page.getByTestId("support-new-sheet");
    await sheet.getByTestId("support-subject").fill("Tax on EU invoices");
    await sheet.getByTestId("support-category").selectOption("billing");
    await sheet.getByTestId("support-priority-select").selectOption("urgent");
    await sheet.getByTestId("support-body").fill("Which rate applies?");
    await sheet.getByTestId("support-submit").click();
    await expect(page.getByTestId("support-row-1043")).toContainText("Tax on EU invoices");
    const sent = gw.called("POST", "/app/v1/support/tickets")[0]?.body as { account_id: string; priority: string };
    expect(sent.account_id).toBe(ACCOUNT);
    expect(sent.priority).toBe("urgent");

    await page.getByTestId("support-row-1042").click();
    const detail = page.getByTestId("support-ticket");
    await expect(detail.getByTestId("support-thread")).toContainText("Since this morning.");
    await expect(detail.getByTestId("support-due")).toBeVisible();
    await detail.getByTestId("support-reply").fill("Any news?");
    await detail.getByTestId("support-send-reply").click();
    await expect(detail.getByTestId("support-thread")).toContainText("Any news?");

    await detail.getByTestId("support-solve").click();
    await expect(detail.getByTestId("support-reopen")).toBeVisible();
    await detail.getByTestId("support-reopen").click();
    await expect(detail.getByTestId("support-solve")).toBeVisible();
    expect(tickets.find((t) => t.number === 1042)?.status).toBe("open");
  });

  test("a closed ticket takes no reply, and an internal note never reaches the person", async ({ page }) => {
    const gw = productGateway(new MockGateway()).gw;
    helpDesk(gw, [ticket({ status: "closed", messages: [
      { id: "m1", author_kind: "customer", author_name: "Ada", body: "Question", internal: false, create_time: NOW },
      { id: "m2", author_kind: "operator", author_name: "Sam", body: "SECRET-TEAM-NOTE", internal: true, create_time: NOW },
    ] })]);
    await gw.install(page);
    await signInAsAda(page);
    await page.goto("/app/support");
    await page.getByTestId("support-row-1042").click();
    await expect(page.getByTestId("support-closed")).toBeVisible();
    await expect(page.getByTestId("support-reply")).toHaveCount(0);
    expect(await page.content()).not.toContain("SECRET-TEAM-NOTE");
  });

  test("an answer the person has not read is marked new until they open the ticket, and the changes are in the thread", async ({ page }) => {
    const gw = productGateway(new MockGateway()).gw;
    helpDesk(gw, [ticket({ status: "pending", unread: true, last_author: "operator", events: [
      { id: "e1", kind: "status", actor_kind: "operator", actor_name: "Sam", from_value: "open", to_value: "pending", create_time: NOW },
      { id: "e2", kind: "priority", actor_kind: "operator", actor_name: "Sam", from_value: "normal", to_value: "high", create_time: NOW },
      { id: "e3", kind: "status", actor_kind: "system", actor_name: "", from_value: "pending", to_value: "closed", create_time: "2026-11-04T09:00:00Z" },
    ] })]);
    await gw.install(page);
    await signInAsAda(page);
    await page.goto("/app/support");
    await expect(page.getByTestId("support-row-1042").getByTestId("support-unread")).toHaveText("New");
    await page.getByTestId("support-row-1042").click();
    const detail = page.getByTestId("support-ticket");
    await expect(detail.getByTestId("support-event-status").first()).toContainText("Sam");
    await expect(detail.getByTestId("support-event-status").first()).toContainText("Status: Waiting for us → Waiting for you");
    await expect(detail.getByTestId("support-event-priority")).toContainText("Priority: Normal → High");
    await expect(detail.getByTestId("support-event-status").last()).toContainText("System");
    await expect(detail.getByTestId("support-event-status").last()).toContainText("Waiting for you → Closed");
    // Reading cleared the mark: the list is read again and the row loses it.
    await expect(page.getByTestId("support-row-1042").getByTestId("support-unread")).toHaveCount(0);
  });

  test("the new ticket sheet says that the answer times count business hours", async ({ page }) => {
    const gw = productGateway(new MockGateway()).gw;
    helpDesk(gw, []);
    await gw.install(page);
    await signInAsAda(page);
    await page.goto("/app/support");
    await page.getByTestId("support-new").click();
    await expect(page.getByTestId("support-new-sheet")).toContainText("Monday to Friday, 09:00 to 18:00");
    await expect(page.getByTestId("support-priority-select")).toContainText("answer within 1 business hour");
  });

  test("a ticket is in the address and comes back on reload", async ({ page }) => {
    const gw = productGateway(new MockGateway()).gw;
    helpDesk(gw, [ticket()]);
    await gw.install(page);
    await signInAsAda(page);
    await page.goto("/app/support");
    await page.getByTestId("support-row-1042").click();
    await expect(page).toHaveURL(/sheet=ticket/);
    await page.reload();
    await expect(page.getByTestId("support-ticket")).toBeVisible();
  });
});

test.describe("support: the operator's inbox", () => {
  test("answers publicly and with an internal note, and sets status, priority and assignee", async ({ page }) => {
    const gw = new MockGateway();
    const tickets = helpDesk(gw, [ticket(), ticket({ id: "c0000000-0000-4000-8000-000000000002", number: 1041, subject: "Old question", status: "solved", sla_breached: false })]);
    await gw.install(page);
    await signInAsOperator(page);
    await page.goto("/support");
    // The default filter is the active tickets: the solved one is not in the queue.
    await expect(page.getByTestId("inbox-row-1042")).toBeVisible();
    await expect(page.getByTestId("inbox-row-1041")).toHaveCount(0);

    await page.getByTestId("inbox-row-1042").click();
    const detail = page.getByTestId("inbox-ticket");
    await detail.getByTestId("ticket-priority").selectOption("urgent");
    await detail.getByTestId("ticket-assignee").selectOption("sam@example.com");
    await expect.poll(() => tickets[0]?.assignee).toBe("sam@example.com");
    expect(tickets[0]?.priority).toBe("urgent");
    // The changes land in the thread's history as the operator makes them.
    await expect(detail.getByTestId("support-event-priority")).toContainText("user1@example.com");
    await expect(detail.getByTestId("support-event-priority")).toContainText("Priority: High → Urgent");
    await expect(detail.getByTestId("support-event-assignee")).toContainText("Assignee: Unassigned → sam@example.com");

    await detail.getByTestId("inbox-reply").fill("Looks like the Paddle outage.");
    await detail.getByTestId("inbox-internal").click();
    await detail.getByTestId("inbox-send").click();
    await expect(detail.getByTestId("support-message-operator-internal")).toContainText("Paddle outage");
    expect(tickets[0]?.status).toBe("open");
    expect(tickets[0]?.first_response_time).toBeNull();

    await detail.getByTestId("inbox-reply").fill("We are on it.");
    await detail.getByTestId("inbox-send").click();
    await expect(detail.getByTestId("support-message-operator")).toContainText("We are on it.");
    await expect.poll(() => tickets[0]?.status).toBe("pending");
    expect(tickets[0]?.first_response_time).not.toBeNull();
    const reply = gw.called("POST", /\/admin\/v1\/support\/tickets\/.*\/replies/);
    expect(reply.map((r) => r.body?.internal)).toEqual([true, false]);
  });

  test("the late filter finds tickets whose first answer is overdue", async ({ page }) => {
    const gw = new MockGateway();
    helpDesk(gw, [ticket({ sla_breached: true }), ticket({ id: "c0000000-0000-4000-8000-000000000003", number: 1050, subject: "Fresh one" })]);
    await gw.install(page);
    await signInAsOperator(page);
    await page.goto("/support");
    await expect(page.getByTestId("inbox-row-1050")).toBeVisible();
    await expect(page.getByTestId("inbox-row-1042").getByTestId("support-late")).toBeVisible();
    // The choice waits for Apply: nothing changes until it is pressed.
    await page.getByRole("button", { name: "Only late", exact: true }).click();
    await page.getByRole("listbox", { name: "Only late" }).getByRole("option", { name: "Only late" }).click();
    await expect(page.getByTestId("inbox-row-1050")).toBeVisible();
    await page.getByTestId("filter-apply").click();
    await expect(page.getByTestId("inbox-row-1050")).toHaveCount(0);
    await expect(page.getByTestId("inbox-row-1042")).toBeVisible();
  });

  test("ticked tickets get the same change, a failure is named and the others still change", async ({ page }) => {
    const gw = new MockGateway();
    const a = ticket();
    const b = ticket({ id: "c0000000-0000-4000-8000-000000000002", number: 1043, subject: "Second" });
    const c = ticket({ id: "c0000000-0000-4000-8000-000000000003", number: 1044, subject: "Third" });
    helpDesk(gw, [a, b, c]);
    // The second ticket's update is refused.
    gw.on((call) => (call.method === "PATCH" && call.path.endsWith(b.id) ? { status: 400, body: { error: { code: "INVALID_ARGUMENT", key: "plans.request.invalid", message: "no" } } } : undefined));
    await gw.install(page);
    await signInAsOperator(page);
    await page.goto("/support");
    await expect(page.getByTestId("bulk-bar")).toHaveCount(0);
    await page.getByTestId("inbox-row-1042").getByTestId("row-check").click();
    await page.getByTestId("inbox-row-1043").getByTestId("row-check").click();
    await page.getByTestId("inbox-row-1044").getByTestId("row-check").click();
    await expect(page.getByTestId("bulk-count")).toHaveText("3 selected");
    await page.getByTestId("bulk-priority").selectOption("urgent");
    await page.getByTestId("bulk-assignee").selectOption("sam@example.com");
    await page.getByTestId("bulk-apply").click();
    await expect(page.getByTestId("bulk-note")).toContainText("2 done, 1 not changed");
    await expect(page.getByTestId("bulk-note")).toContainText("#1043");
    expect([a.priority, c.priority, b.priority]).toEqual(["urgent", "urgent", "high"]);
    expect(a.assignee).toBe("sam@example.com");
    // Only the one that failed stays ticked.
    await expect(page.getByTestId("bulk-count")).toHaveText("1 selected");
    await page.getByTestId("bulk-clear").click();
    await expect(page.getByTestId("bulk-bar")).toHaveCount(0);
  });
});

test.describe("support: tags, canned replies, the report and the badges", () => {
  test("tags are the operator's: added, removed, filtered, in the history, and never in the person's view", async ({ page }) => {
    const gw = new MockGateway();
    const tickets = helpDesk(gw, [ticket(), ticket({ id: "c0000000-0000-4000-8000-000000000002", number: 1043, subject: "Second", tags: ["vat"] })]);
    await gw.install(page);
    await signInAsOperator(page);
    await page.goto("/support");
    await expect(page.getByTestId("inbox-row-1043").getByTestId("row-tag-vat")).toBeVisible();
    await page.getByTestId("inbox-row-1042").click();
    const detail = page.getByTestId("inbox-ticket");
    await detail.getByTestId("tag-input").fill("Refund");
    await detail.getByTestId("tag-input").press("Enter");
    await expect(detail.getByTestId("tag-refund")).toBeVisible();
    expect(tickets[0]?.tags).toEqual(["refund"]);
    expect(gw.called("PATCH", /\/admin\/v1\/support\/tickets\//).at(-1)?.body).toEqual({ tags: ["refund"] });
    await expect(detail.getByTestId("support-event-tags")).toContainText("Tags: — → refund");
    await detail.getByTestId("tag-input").fill("vat");
    await detail.getByTestId("tag-input").press("Enter");
    await expect(detail.getByTestId("support-event-tags").last()).toContainText("Tags: refund → refund, vat");
    await detail.getByTestId("tag-remove-refund").click();
    await expect.poll(() => tickets[0]?.tags.join(",")).toBe("vat");
    // The queue can be narrowed to a tag (the box applies when it is left).
    await page.keyboard.press("Escape");
    await page.getByTestId("filter-tag").fill("vat");
    await page.getByTestId("filter-tag").press("Enter");
    await expect(page.getByTestId("inbox-row-1043")).toBeVisible();
    expect(gw.called("GET", "/admin/v1/support/tickets").at(-1)?.query.get("tag")).toBe("vat");
  });

  test("a canned reply is pasted into the answer, and the team's list is made, changed and deleted in a dialog", async ({ page }) => {
    const gw = new MockGateway();
    helpDesk(gw, [ticket()]);
    let replies = [{ id: "r1", title: "Asking for logs", body: "Could you send the delivery ids?", updated_by: "Sam", update_time: NOW }];
    gw.on((c) => {
      if (c.path === "/admin/v1/support/canned-replies" && c.method === "GET") return { body: { replies } };
      if (c.path === "/admin/v1/support/canned-replies" && c.method === "POST") {
        replies = [...replies, { id: "r2", title: String(c.body?.title), body: String(c.body?.body), updated_by: "user1@example.com", update_time: NOW }];
        return { status: 201, body: { reply: replies.at(-1) } };
      }
      if (c.path === "/admin/v1/support/canned-replies/r1" && c.method === "PUT") {
        replies = replies.map((r) => (r.id === "r1" ? { ...r, title: String(c.body?.title), body: String(c.body?.body) } : r));
        return { body: { reply: replies[0] } };
      }
      if (c.path === "/admin/v1/support/canned-replies/r2" && c.method === "DELETE") {
        replies = replies.filter((r) => r.id !== "r2");
        return { status: 204, body: {} };
      }
      return undefined;
    });
    await gw.install(page);
    await signInAsOperator(page);
    await page.goto("/support");
    await page.getByTestId("inbox-row-1042").click();
    const detail = page.getByTestId("inbox-ticket");
    await detail.getByTestId("inbox-reply").fill("Thanks for writing.");
    await detail.getByTestId("inbox-canned").selectOption("r1");
    await expect(detail.getByTestId("inbox-reply")).toHaveValue("Thanks for writing.\n\nCould you send the delivery ids?");
    await expect(detail.getByTestId("inbox-canned")).toHaveValue("");

    await detail.getByTestId("canned-manage").click();
    const dialog = page.getByTestId("canned-dialog");
    await expect(dialog.getByTestId("canned-r1")).toContainText("Asking for logs");
    await dialog.getByTestId("canned-title").fill("Closing");
    await dialog.getByTestId("canned-body").fill("Marking this solved.");
    await dialog.getByTestId("canned-save").click();
    await expect(dialog.getByTestId("canned-r2")).toContainText("Closing");
    expect(gw.called("POST", "/admin/v1/support/canned-replies")[0]?.body).toEqual({ title: "Closing", body: "Marking this solved." });
    await dialog.getByTestId("canned-edit-r1").click();
    await expect(dialog.getByTestId("canned-title")).toHaveValue("Asking for logs");
    await dialog.getByTestId("canned-title").fill("Asking for webhook logs");
    await dialog.getByTestId("canned-save").click();
    await expect(dialog.getByTestId("canned-r1")).toContainText("Asking for webhook logs");
    await dialog.getByTestId("canned-delete-r2").click();
    await expect(dialog.getByTestId("canned-r2")).toContainText("Delete the canned reply “Closing”?");
    await dialog.getByTestId("canned-delete-yes-r2").click();
    await expect(dialog.getByTestId("canned-r2")).toHaveCount(0);
  });

  test("the report shows the queue and the first answers, and the sidebar counts what waits", async ({ page }) => {
    const gw = new MockGateway();
    helpDesk(gw, [ticket()]);
    gw.on((c) =>
      c.method === "GET" && c.path === "/admin/v1/support/report"
        ? { body: { waiting: 4, breached: 1, by_status: [{ value: "open", count: 4 }, { value: "pending", count: 2 }, { value: "solved", count: 9 }], by_priority: [{ value: "high", count: 1 }, { value: "normal", count: 5 }],
            window_days: 30, opened: 20, answered: 18, answered_on_time: 15, median_first_answer_seconds: 5400, average_first_answer_seconds: 14_400 } }
        : undefined,
    );
    await gw.install(page);
    await signInAsOperator(page);
    await page.goto("/support");
    const report = page.getByTestId("support-report");
    await expect(report.getByTestId("report-waiting")).toHaveText("4");
    await expect(report.getByTestId("report-late")).toHaveText("1");
    await expect(report.getByTestId("report-median")).toHaveText("2h");
    await expect(report.getByTestId("report-average")).toContainText("4h");
    await expect(report.getByTestId("report-on-time")).toHaveText("83%");
    await expect(report.getByTestId("report-by-status")).toContainText("Waiting for us 4");
    await expect(report.getByTestId("report-by-priority")).toContainText("Normal 5");
    await expect(page.getByTestId("nav-badge-supportInbox")).toHaveText("4");
  });

  test("the help page's link counts the tickets with news, and nothing when there are none", async ({ page }) => {
    const gw = productGateway(new MockGateway()).gw;
    helpDesk(gw, [ticket({ unread: true })]);
    let unread = 2;
    gw.on((c) => (c.method === "GET" && c.path === "/app/v1/support/unread" ? { body: { unread } } : undefined));
    await gw.install(page);
    await signInAsAda(page);
    await expect(page.getByTestId("nav-badge-support")).toHaveText("2");
    unread = 0;
    await page.goto("/app/support");
    await expect(page.getByTestId("support-row-1042")).toBeVisible();
    await expect(page.getByTestId("nav-badge-support")).toHaveCount(0);
  });
});
