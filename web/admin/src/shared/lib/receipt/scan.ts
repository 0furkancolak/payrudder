import type { Currency } from "../currencies";
import { parseReceipt, type ParsedReceipt } from "./parse";

/**
 * Reads a receipt or an invoice file in the browser: the text layer of a PDF (pdf.js), OCR for a photo or a scanned PDF (Tesseract.js), then the parser. The file
 * never leaves the browser to be read; nothing is sent anywhere until a person confirms an expense and the file is stored as its attachment.
 *
 * Both libraries are loaded the first time a file is scanned, as their own chunks. pdf.js and its worker are served from the console (same origin). Tesseract's
 * worker, its WebAssembly core and the language data (English and Turkish, `OCR_LANGS`) come from jsdelivr, pinned to the installed version of the package (the
 * library's own default); the page's Content-Security-Policy allows that one origin for scripts and fetches (web/admin/public/_headers).
 */

export type ScanKind = "pdf" | "image";

export interface ScanResult {
  kind: ScanKind;
  /** How the text was got: the text layer, or OCR. */
  source: "text" | "ocr";
  text: string;
  parsed: ParsedReceipt;
}

/** The OCR language packs: English and Turkish. */
export const OCR_LANGS = ["eng", "tur"];
/** The largest file that is scanned; the same cap the attachment has. */
export const MAX_SCAN_BYTES = 8 * 1024 * 1024;
/** A PDF whose text layer is shorter than this is taken for a scan and read with OCR. */
const MIN_TEXT = 40;
const MAX_PAGES = 3;

/** The kind of a file from its first bytes (the name and the type the browser gave are not trusted), or undefined when it cannot be scanned. */
export async function sniff(file: Blob): Promise<ScanKind | undefined> {
  const head = new Uint8Array(await file.slice(0, 8).arrayBuffer());
  const is = (...b: number[]) => b.every((v, i) => head[i] === v);
  if (is(0x25, 0x50, 0x44, 0x46, 0x2d)) return "pdf";
  if (is(0x89, 0x50, 0x4e, 0x47) || is(0xff, 0xd8, 0xff)) return "image";
  return undefined;
}

type PdfjsModule = typeof import("pdfjs-dist");
let pdfjs: Promise<PdfjsModule> | undefined;
function loadPdfjs(): Promise<PdfjsModule> {
  pdfjs ??= (async () => {
    const [lib, worker] = await Promise.all([import("pdfjs-dist"), import("pdfjs-dist/build/pdf.worker.min.mjs?url")]);
    lib.GlobalWorkerOptions.workerSrc = worker.default;
    return lib;
  })();
  return pdfjs;
}

type TesseractWorker = import("tesseract.js").Worker;
let ocr: Promise<TesseractWorker> | undefined;
function loadOcr(): Promise<TesseractWorker> {
  ocr ??= import("tesseract.js").then((t) => t.createWorker(OCR_LANGS));
  return ocr;
}

/** Releases the OCR worker (its memory is large); the next image starts a new one. */
export async function releaseOcr(): Promise<void> {
  const w = await ocr;
  ocr = undefined;
  await w?.terminate();
}

/** Groups the text items of a PDF page into lines by their vertical position, left to right. */
function pageLines(items: { str: string; transform: number[] }[]): string {
  const rows = new Map<number, { x: number; s: string }[]>();
  for (const it of items) {
    if (!it.str.trim()) continue;
    const y = Math.round((it.transform[5] ?? 0) / 3);
    const row = rows.get(y) ?? [];
    row.push({ x: it.transform[4] ?? 0, s: it.str });
    rows.set(y, row);
  }
  return [...rows.entries()]
    .sort((a, b) => b[0] - a[0])
    .map(([, r]) => r.sort((a, b) => a.x - b.x).map((c) => c.s).join(" "))
    .join("\n");
}

async function pdfText(data: ArrayBuffer): Promise<{ text: string; firstPage: () => Promise<Blob | undefined> }> {
  const lib = await loadPdfjs();
  const doc = await lib.getDocument({ data: new Uint8Array(data.slice(0)) }).promise;
  let text = "";
  for (let i = 1; i <= Math.min(doc.numPages, MAX_PAGES); i++) {
    const page = await doc.getPage(i);
    const content = await page.getTextContent();
    text += pageLines(content.items as { str: string; transform: number[] }[]) + "\n";
  }
  // A scanned PDF has no text: its first page is drawn to a canvas for OCR.
  const firstPage = async (): Promise<Blob | undefined> => {
    const page = await doc.getPage(1);
    const viewport = page.getViewport({ scale: 2 });
    const canvas = document.createElement("canvas");
    canvas.width = viewport.width;
    canvas.height = viewport.height;
    await page.render({ canvas, viewport }).promise;
    return new Promise((resolve) => canvas.toBlob((b) => resolve(b ?? undefined), "image/png"));
  };
  return { text, firstPage };
}

async function recognize(image: Blob): Promise<string> {
  const worker = await loadOcr();
  const { data } = await worker.recognize(image);
  return data.text;
}

/**
 * Scans one file. Throws an Error with the message "unsupported" (neither a PDF nor a PNG or JPEG), "too_large" or "unreadable" (the file is damaged or no
 * text could be read from it): the caller shows a message of its own and lets the person fill the row by hand.
 */
export async function scanFile(file: File, opts: { defaultCurrency?: Currency } = {}): Promise<ScanResult> {
  if (file.size > MAX_SCAN_BYTES) throw new Error("too_large");
  const kind = await sniff(file);
  if (!kind) throw new Error("unsupported");
  const parse = (text: string) => parseReceipt(text, { filename: file.name, defaultCurrency: opts.defaultCurrency });
  try {
    if (kind === "pdf") {
      const { text, firstPage } = await pdfText(await file.arrayBuffer());
      if (text.replace(/\s/g, "").length >= MIN_TEXT) return { kind, source: "text", text, parsed: parse(text) };
      const image = await firstPage();
      if (!image) throw new Error("unreadable");
      const read = await recognize(image);
      return { kind, source: "ocr", text: read, parsed: parse(read) };
    }
    const read = await recognize(file);
    return { kind, source: "ocr", text: read, parsed: parse(read) };
  } catch (e) {
    if (e instanceof Error && ["unsupported", "too_large", "unreadable"].includes(e.message)) throw e;
    throw new Error("unreadable");
  }
}
