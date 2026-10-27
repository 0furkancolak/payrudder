import { describe, expect, it } from "vitest";
import { answerConfirm, askConfirm, pendingConfirm } from "./confirm";

describe("askConfirm", () => {
  it("resolves with the answer and clears the question", async () => {
    const yes = askConfirm({ body: "Sure?" });
    expect(pendingConfirm.value?.body).toBe("Sure?");
    answerConfirm(true);
    await expect(yes).resolves.toBe(true);
    expect(pendingConfirm.value).toBeNull();
    const no = askConfirm({ body: "Sure?" });
    answerConfirm(false);
    await expect(no).resolves.toBe(false);
  });
  it("answers an earlier question with no when another is asked", async () => {
    const first = askConfirm({ body: "one" });
    const second = askConfirm({ body: "two" });
    await expect(first).resolves.toBe(false);
    answerConfirm(true);
    await expect(second).resolves.toBe(true);
  });
});
