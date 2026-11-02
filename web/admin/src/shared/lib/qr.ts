import qrcode from "qrcode-generator";

/**
 * A QR code drawn in the browser, from the module matrix of `qrcode-generator` (MIT, no dependencies). The text never leaves the page: the
 * authenticator secret in an `otpauth://` address must not be sent to a QR service. Error correction level M, byte mode.
 */

/** The width of the empty border around the code, in modules. The QR standard asks for four; a scanner needs it to find the code on a dark page. */
export const QUIET_ZONE = 4;

/** The dark modules of the code, row by row. */
export function qrMatrix(text: string): boolean[][] {
  const code = qrcode(0, "M");
  code.addData(text, "Byte");
  code.make();
  const n = code.getModuleCount();
  return Array.from({ length: n }, (_, r) => Array.from({ length: n }, (_, c) => code.isDark(r, c)));
}

export interface QrShape {
  /** Modules along one side of the code itself. */
  modules: number;
  /** The side of the drawing in modules: the code plus the quiet zone on every side. */
  size: number;
  /** An SVG path: one rectangle per horizontal run of dark modules, in module units. */
  path: string;
}

export function qrShape(text: string, quiet = QUIET_ZONE): QrShape {
  const m = qrMatrix(text);
  const runs: string[] = [];
  m.forEach((row, y) => {
    let x = 0;
    while (x < row.length) {
      if (!row[x]) {
        x++;
        continue;
      }
      const start = x;
      while (x < row.length && row[x]) x++;
      runs.push(`M${start + quiet} ${y + quiet}h${x - start}v1h-${x - start}z`);
    }
  });
  return { modules: m.length, size: m.length + 2 * quiet, path: runs.join("") };
}
