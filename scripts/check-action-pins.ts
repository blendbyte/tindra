import { readdir } from "node:fs/promises";
import { join } from "node:path";

// Inspect YAML nodes so quoted keys, aliases, and reusable workflows are covered.
export function unpinnedUses(document: unknown): string[] {
  const failures: string[] = [];
  function check(node: any, location: string) {
    if (!node || typeof node !== "object" || !("uses" in node)) return;
    const ref = node.uses;
    if (typeof ref === "string" && ref.startsWith("./")) return;
    const pinned = typeof ref === "string" && (
      ref.startsWith("docker://")
        ? /^docker:\/\/[^\s@]+@sha256:[a-fA-F0-9]{64}$/.test(ref)
        : /^[\w.-]+\/[\w./-]+@[a-fA-F0-9]{40}$/.test(ref)
    );
    if (!pinned) failures.push(`${location}: ${String(ref)}`);
  }
  const workflow = document as any;
  for (const [name, job] of Object.entries(workflow?.jobs ?? {}) as [string, any][]) {
    check(job, `jobs.${name}`);
    for (const [index, step] of (job.steps ?? []).entries()) {
      check(step, `jobs.${name}.steps[${index}]`);
    }
  }
  return failures;
}

if (import.meta.main) {
  const directory = process.argv[2] ?? ".github/workflows";
  const files = (await readdir(directory)).filter((file) => /\.ya?ml$/.test(file));
  if (!files.length) throw new Error(`No workflows found in ${directory}`);
  let failures = 0;
  for (const file of files) {
    const path = join(directory, file);
    const document = Bun.YAML.parse(await Bun.file(path).text());
    for (const failure of unpinnedUses(document)) {
      console.error(`${path}: ${failure}: pin GitHub Actions to a full commit SHA and Docker actions to a digest`);
      failures++;
    }
  }
  if (failures) process.exit(1);
  console.log(`All action references in ${files.length} workflows are pinned.`);
}
