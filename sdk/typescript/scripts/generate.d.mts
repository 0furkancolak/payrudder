export interface Generated {
  files: Record<string, string>;
  warnings: string[];
  stats: { operations: number; namespaces: number; paginated: string[] };
}
export function generate(specText: string): Generated;
