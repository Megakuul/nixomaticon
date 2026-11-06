// Copies the license files of every package reported by `pnpm licenses list
// --json` into one directory per package, and writes a summary without build
// paths. Build tools are included too, since separating them from bundled code
// would depend on bundler internals.
import { copyFileSync, mkdirSync, readdirSync, readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";

const [report, destination] = process.argv.slice(2);
const packages = Object.values(JSON.parse(readFileSync(report, "utf8"))).flat();
const summary = [];
for (const item of packages) {
  item.versions.forEach((version, index) => {
    const directory = join(destination, `${item.name}@${version}`);
    mkdirSync(directory, { recursive: true });
    const files = readdirSync(item.paths[index]).filter((name) => /^(licen[cs]e|copying|notice)/i.test(name));
    for (const name of files) copyFileSync(join(item.paths[index], name), join(directory, name));
    if (!files.length)
      writeFileSync(join(directory, "LICENSE.txt"), `${item.name} ${version} declares the ${item.license} license but ships no license file.\n`);
    summary.push({ name: item.name, version, license: item.license, homepage: item.homepage || "" });
  });
}
summary.sort((a, b) => a.name.localeCompare(b.name));
writeFileSync(join(destination, "licenses.json"), JSON.stringify(summary, null, 2) + "\n");
