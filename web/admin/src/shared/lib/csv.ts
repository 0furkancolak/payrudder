/**
 * A CSV file made in the browser. A cell is quoted when it holds a comma, a quote or a line break; a cell a spreadsheet would run as a formula (it starts with
 * = + - @, a tab or a carriage return) gets a leading apostrophe, so a customer named "=HYPERLINK(...)" is text in the file, not a formula.
 */
export function csvCell(v: string | number): string {
  let s = String(v);
  if (/^[=+\-@\t\r]/.test(s) && !/^-?\d+(\.\d+)?$/.test(s)) s = `'${s}`;
  return /[",\r\n]/.test(s) ? `"${s.replaceAll('"', '""')}"` : s;
}

export function toCsv(header: readonly string[], rows: readonly (readonly (string | number)[])[]): string {
  return [header, ...rows].map((r) => r.map(csvCell).join(",")).join("\r\n") + "\r\n";
}

/** Offers the file for download (UTF-8 with a byte-order mark, so Excel reads accents and non-Latin scripts). */
export function downloadCsv(name: string, header: readonly string[], rows: readonly (readonly (string | number)[])[]): void {
  const blob = new Blob(["﻿", toCsv(header, rows)], { type: "text/csv;charset=utf-8" });
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = name;
  document.body.appendChild(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}
