import { describe, expect, it } from "vitest";
import { clearAt, digitsOf, pasteStart, spread } from "./otp";

const empty = ["", "", "", "", "", ""];

describe("the six code boxes", () => {
  it("keeps only digits from whatever is pasted or autofilled", () => {
    expect(digitsOf("123 456")).toBe("123456");
    expect(digitsOf("code: 12-34-56\n")).toBe("123456");
    expect(digitsOf("abc")).toBe("");
    expect(digitsOf("١٢٣")).toBe(""); // other scripts' digits are not accepted: the server wants 0-9
  });

  it("fills the boxes from the one typed in and says which box is next", () => {
    expect(spread(empty, 2, "7")).toEqual({ cells: ["", "", "7", "", "", ""], next: 3 });
    expect(spread(empty, 0, "123456")).toEqual({ cells: ["1", "2", "3", "4", "5", "6"], next: 6 });
  });

  it("drops digits that do not fit and leaves earlier boxes alone", () => {
    expect(spread(["1", "2", "", "", "", ""], 4, "9876")).toEqual({ cells: ["1", "2", "", "", "9", "8"], next: 6 });
  });

  it("starts a whole pasted code at the first box and a partial one where the caret is", () => {
    expect(pasteStart(6, 6, 3)).toBe(0);
    expect(pasteStart(8, 6, 3)).toBe(0);
    expect(pasteStart(2, 6, 3)).toBe(3);
  });

  it("empties one box", () => {
    expect(clearAt(["1", "2", "3", "", "", ""], 1)).toEqual(["1", "", "3", "", "", ""]);
  });
});
