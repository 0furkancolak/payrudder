import { expect, test } from "@playwright/test";
import { MockGateway, signInAsOperator } from "./gateway";
import { ACCOUNT, APP, productGateway, signInAsAda } from "./product";

// The help desk: the organisation's tickets (open one, answer on it, mark it solved, reopen it) and the operator's inbox (filter, answer publicly or leave an
// internal note, set status, priority and assignee). The gateway is mocked with one shared set of tickets, so what one side writes the other reads.

interface File1 {
  id: string;
  filename: string;
  content_type: string;
  size_bytes: number;
  create_time: string;
}
interface Msg {
  id: string;
  author_kind: "customer" | "operator";
  author_name: string;
  body: string;
  internal: boolean;
  create_time: string;
  attachments?: File1[];
}
interface Ev {
  id: string;
  kind: "status" | "priority" | "assignee";
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
  messages: Msg[];
  events: Ev[];
}

const NOW = "2026-10-28T09:00:00Z";
function ticket(over: Partial<Ticket> = {}): Ticket {
  return {
    id: "c0000000-0000-4000-8000-000000000001", number: 1042, account_id: ACCOUNT, account_name: "Ada Labs", app_id: APP, app_name: "Shop", opened_by: "u1", opened_by_name: "Ada",
    subject: "Paddle webhooks are late", category: "integration", priority: "high", status: "open", assignee: "", create_time: NOW, update_time: NOW, last_message_time: NOW,
    last_author: "customer", first_response_due: "2026-10-28T13:00:00Z", first_response_time: null, sla_breached: false, solve_time: null, message_count: 1, unread: false, events: [],
    messages: [{ id: "m1", author_kind: "customer", author_name: "Ada", body: "Since this morning.", internal: false, create_time: NOW }],
    ...over,
  };
}

/**
 * One set of tickets behind both sides. `internal` messages are in the operator's view only, and "unread" is the opener's: the operator never has it. A file
 * sent to a message is recorded on it (its name is read from the multipart body); one whose name starts with "refused" is refused like a full ticket.
 */
function helpDesk(gw: MockGateway, tickets: Ticket[], operators: string[] = []) {
  const view = (t: Ticket, operator: boolean) => {
    const messages = t.messages.filter((m) => operator || !m.internal);
    const rest: Omit<Ticket, "messages" | "events"> & { messages?: Msg[]; events?: Ev[] } = { ...t };
    delete rest.messages;
    delete rest.events;
    return { ticket: { ...rest, unread: operator ? false : t.unread, message_count: messages.filter((m) => !m.internal).length }, messages, events: t.events };
  };
  const find = (path: string) => tickets.find((t) => path.includes(t.id));
  gw.on((c) => {
    if (c.path === "/app/v1/support/summary") return { body: { unread: tickets.filter((t) => t.unread).length } };
    if (c.path === "/admin/v1/support/summary") return { body: { waiting: tickets.filter((t) => t.status === "open").length, sla_breached: tickets.filter((t) => t.sla_breached && t.status === "open").length } };
    if (c.path === "/admin/v1/support/operators") return { body: { operators } };
    const upload = /^\/(app|admin)\/v1\/support\/tickets\/([^/]+)\/messages\/([^/]+)\/attachments$/.exec(c.path);
    if (upload && c.method === "POST") {
      const name = /filename="([^"]+)"/.exec(c.text ?? "")?.[1] ?? "file";
      if (name.startsWith("refused")) return { status: 429, body: { error: { code: "RESOURCE_EXHAUSTED", key: "support.attachment.too_many", message: "a ticket can hold at most 10 files" } } };
      const t = tickets.find((x) => x.id === upload[2])!;
      const file = { id: `f${t.messages.flatMap((m) => m.attachments ?? []).length + 1}`, filename: name, content_type: "image/png", size_bytes: 2048, create_time: NOW };
      (t.messages.find((m) => m.id === upload[3])!.attachments ??= []).push(file);
      return { status: 201, body: file };
    }
    if (/\/support\/tickets\/[^/]+\/attachments\/[^/]+$/.test(c.path) && c.method === "GET") return { body: { url: `https://files.example.test${c.path}`, expires_at: "2026-10-28T09:05:00Z" } };
    if (c.path === "/app/v1/support/tickets" && c.method === "GET") return { body: { tickets: tickets.map((t) => view(t, false).ticket) } };
    if (c.path === "/app/v1/support/tickets" && c.method === "POST") {
      const b = c.body as { subject: string; category: string; priority?: string; body: string; app_id?: string };
      const t = ticket({ id: `c0000000-0000-4000-8000-00000000009${tickets.length}`, number: 1042 + tickets.length, subject: b.subject, category: b.category, priority: b.priority ?? "normal", app_id: b.app_id ?? "", app_name: b.app_id ? "Shop" : "",
        messages: [{ id: "n1", author_kind: "customer", author_name: "Ada", body: b.body, internal: false, create_time: NOW }] });
      tickets.unshift(t);
      return { status: 201, body: { ticket: view(t, false).ticket, message: t.messages[0] } };
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
      return { status: 201, body: { ticket: view(t, false).ticket, message: t.messages.at(-1) } };
    }
    if (c.path.endsWith("/status") && c.path.startsWith("/app/v1/")) {
      const t = find(c.path)!;
      t.status = String(c.body?.status);
      return { body: { ticket: view(t, false).ticket } };
    }
    if (c.path === "/admin/v1/support/tickets" && c.method === "GET") {
      const status = c.query.get("status");
      const list = tickets.filter((t) => (!status ? true : status === "active" ? ["open", "pending"].includes(t.status) : t.status === status) && (c.query.get("sla_breached") !== "true" || t.sla_breached));
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
      return { status: 201, body: { ticket: view(t, true).ticket, message: t.messages.at(-1) } };
    }
    if (c.path.startsWith("/admin/v1/support/tickets/") && c.method === "PATCH") {
      const t = find(c.path)!;
      const patch = Object.fromEntries(Object.entries(c.body ?? {}).filter(([, v]) => v !== undefined));
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

  test("files go with a new ticket and with a reply, the thread shows them and a file opens through a short-lived link", async ({ page }) => {
    const gw = productGateway(new MockGateway()).gw;
    const tickets = helpDesk(gw, []);
    await page.addInitScript(() => {
      (window as unknown as { opened: string[] }).opened = [];
      window.open = ((url: string) => ((window as unknown as { opened: string[] }).opened.push(String(url)), null)) as typeof window.open;
    });
    await gw.install(page);
    await signInAsAda(page);
    await page.goto("/app/support");
    await page.getByTestId("support-new").click();
    const sheet = page.getByTestId("support-new-sheet");
    await sheet.getByTestId("support-subject").fill("Checkout fails");
    await sheet.getByTestId("support-body").fill("See the screenshot.");
    await expect(sheet.getByTestId("attach-picker")).toContainText("up to 5 MB each, 10 per ticket");
    await sheet.getByTestId("attach-input").setInputFiles({ name: "checkout.png", mimeType: "image/png", buffer: Buffer.from("png") });
    await expect(sheet.getByTestId("attach-chosen")).toContainText("checkout.png");
    await sheet.getByTestId("support-submit").click();
    await expect(page.getByTestId("support-row-1042")).toContainText("Checkout fails");
    await expect(sheet).toHaveCount(0);
    expect(gw.called("POST", /\/app\/v1\/support\/tickets\/.*\/messages\/n1\/attachments/)).toHaveLength(1);
    expect(tickets[0]?.messages[0]?.attachments?.map((a) => a.filename)).toEqual(["checkout.png"]);

    await page.getByTestId("support-row-1042").click();
    const detail = page.getByTestId("support-ticket");
    await expect(detail.getByTestId("support-attachment")).toContainText("checkout.png");
    await expect(detail.getByTestId("support-attachment")).toContainText("2 KB");
    await detail.getByTestId("support-reply").fill("A second screenshot.");
    await detail.getByTestId("attach-input").setInputFiles([
      { name: "second.png", mimeType: "image/png", buffer: Buffer.from("png") },
      { name: "log.txt", mimeType: "text/plain", buffer: Buffer.from("log") },
    ]);
    await detail.getByTestId("support-send-reply").click();
    await expect(detail.getByTestId("support-attachment")).toHaveCount(3);
    await expect(detail.getByTestId("attach-chosen")).toHaveCount(0);

    await detail.getByTestId("support-attachment").first().click();
    await expect.poll(() => page.evaluate(() => (window as unknown as { opened: string[] }).opened)).toEqual([expect.stringMatching(/^https:\/\/files\.example\.test\/app\/v1\/support\/tickets\/.*\/attachments\/f\d$/)]);
  });

  test("a file the page can see is wrong is not sent, and one the help desk refuses is named while the message stays", async ({ page }) => {
    const gw = productGateway(new MockGateway()).gw;
    helpDesk(gw, [ticket()]);
    await gw.install(page);
    await signInAsAda(page);
    await page.goto("/app/support");
    await page.getByTestId("support-row-1042").click();
    const detail = page.getByTestId("support-ticket");
    await detail.getByTestId("support-reply").fill("Here are the files.");
    await detail.getByTestId("attach-input").setInputFiles([
      { name: "archive.zip", mimeType: "application/zip", buffer: Buffer.from("zip") },
      { name: "refused-1.png", mimeType: "image/png", buffer: Buffer.from("png") },
      { name: "fine.png", mimeType: "image/png", buffer: Buffer.from("png") },
    ]);
    await detail.getByTestId("support-send-reply").click();
    await expect(detail.getByTestId("support-thread")).toContainText("Here are the files.");
    const failures = detail.getByTestId("attach-failures");
    await expect(failures).toContainText("archive.zip: Only png, jpeg, pdf and plain text files can be attached.");
    await expect(failures).toContainText("refused-1.png: A ticket can hold at most 10 files.");
    await expect(failures).not.toContainText("fine.png");
    // The zip never left the page; the refused one was tried; the good one is on the message.
    expect(gw.called("POST", /attachments$/)).toHaveLength(2);
    await expect(detail.getByTestId("support-attachment")).toContainText("fine.png");
  });

  test("the sidebar counts the tickets with news and the count goes when they are read", async ({ page }) => {
    const gw = productGateway(new MockGateway()).gw;
    helpDesk(gw, [ticket({ status: "pending", unread: true, last_author: "operator" }), ticket({ id: "c0000000-0000-4000-8000-000000000002", number: 1043, subject: "Another", unread: true }), ticket({ id: "c0000000-0000-4000-8000-000000000003", number: 1044, subject: "Seen" })]);
    await gw.install(page);
    await signInAsAda(page);
    await page.goto("/app/support");
    const badge = page.getByTestId("nav-badge-support").first();
    await expect(badge).toHaveText(/^2/);
    await expect(badge).toHaveAttribute("title", "2 with news");
    await page.getByTestId("support-row-1042").click();
    await expect(page.getByTestId("support-ticket")).toBeVisible();
    await expect(badge).toHaveText(/^1/);
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
    await detail.getByTestId("ticket-assignee").fill("Sam");
    await detail.getByTestId("ticket-assignee").press("Tab");
    await expect.poll(() => tickets[0]?.assignee).toBe("Sam");
    expect(tickets[0]?.priority).toBe("urgent");
    // The changes land in the thread's history as the operator makes them.
    await expect(detail.getByTestId("support-event-priority")).toContainText("user1@example.com");
    await expect(detail.getByTestId("support-event-priority")).toContainText("Priority: High → Urgent");
    await expect(detail.getByTestId("support-event-assignee")).toContainText("Assignee: Unassigned → Sam");

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

  test("the inbox count in the sidebar is the tickets waiting, and turns red with late ones", async ({ page }) => {
    const gw = new MockGateway();
    helpDesk(gw, [ticket({ sla_breached: true }), ticket({ id: "c0000000-0000-4000-8000-000000000002", number: 1043, subject: "Second" }), ticket({ id: "c0000000-0000-4000-8000-000000000003", number: 1044, subject: "Answered", status: "pending" })]);
    await gw.install(page);
    await signInAsOperator(page);
    await page.goto("/support");
    const badge = page.getByTestId("nav-badge-supportInbox").first();
    await expect(badge).toHaveText(/^2/);
    await expect(badge).toHaveAttribute("title", "2 waiting for an answer, 1 late");
  });

  test("a ticket is assigned to one of the operators, from a list; files go with the answer and a note's file is the operator's", async ({ page }) => {
    const gw = new MockGateway();
    const tickets = helpDesk(gw, [ticket({ messages: [
      { id: "m1", author_kind: "customer", author_name: "Ada", body: "Since this morning.", internal: false, create_time: NOW, attachments: [{ id: "f9", filename: "ada.png", content_type: "image/png", size_bytes: 3000, create_time: NOW }] },
    ] })], ["user1@example.com", "sam@example.com"]);
    await page.addInitScript(() => {
      (window as unknown as { opened: string[] }).opened = [];
      window.open = ((url: string) => ((window as unknown as { opened: string[] }).opened.push(String(url)), null)) as typeof window.open;
    });
    await gw.install(page);
    await signInAsOperator(page);
    await page.goto("/support");
    await page.getByTestId("inbox-row-1042").click();
    const detail = page.getByTestId("inbox-ticket");
    const assignee = detail.getByTestId("ticket-assignee");
    await expect(assignee.locator("option")).toHaveText(["Unassigned", "user1@example.com", "sam@example.com"]);
    await assignee.selectOption("sam@example.com");
    await expect.poll(() => tickets[0]?.assignee).toBe("sam@example.com");
    await expect(detail.getByTestId("support-event-assignee")).toContainText("Unassigned → sam@example.com");

    await detail.getByTestId("support-attachment").first().click();
    await expect.poll(() => page.evaluate(() => (window as unknown as { opened: string[] }).opened)).toEqual(["https://files.example.test/admin/v1/support/tickets/c0000000-0000-4000-8000-000000000001/attachments/f9"]);

    await detail.getByTestId("inbox-reply").fill("Try the attached build.");
    await detail.getByTestId("attach-input").setInputFiles({ name: "build.pdf", mimeType: "application/pdf", buffer: Buffer.from("pdf") });
    await detail.getByTestId("inbox-send").click();
    await expect(detail.getByTestId("support-message-operator")).toContainText("build.pdf");
    expect(gw.called("POST", /\/admin\/v1\/support\/tickets\/.*\/messages\/m2\/attachments/)).toHaveLength(1);
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
    await page.getByTestId("bulk-assignee").fill("Sam");
    await page.getByTestId("bulk-apply").click();
    await expect(page.getByTestId("bulk-note")).toContainText("2 done, 1 not changed");
    await expect(page.getByTestId("bulk-note")).toContainText("#1043");
    expect([a.priority, c.priority, b.priority]).toEqual(["urgent", "urgent", "high"]);
    expect(a.assignee).toBe("Sam");
    // Only the one that failed stays ticked.
    await expect(page.getByTestId("bulk-count")).toHaveText("1 selected");
    await page.getByTestId("bulk-clear").click();
    await expect(page.getByTestId("bulk-bar")).toHaveCount(0);
  });
});

