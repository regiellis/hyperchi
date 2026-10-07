import { transform } from "esbuild";
import { copyFile, mkdir, readFile, writeFile } from "node:fs/promises";
const copies = [
	["three/build/three.module.js", "js/three/three.module.js"],
	["three/build/three.core.js", "js/three/three.core.js"],
	["three/LICENSE", "js/three/LICENSE"],
	[
		"@fontsource-variable/space-grotesk/files/space-grotesk-latin-wght-normal.woff2",
		"fonts/space-grotesk-latin.woff2",
	],
	["@fontsource-variable/space-grotesk/LICENSE", "fonts/OFL.txt"],
];
for (const [source, destination] of copies) {
	const path = `static/${destination}`;
	await mkdir(path.slice(0, path.lastIndexOf("/")), { recursive: true });
	if (source.endsWith(".js")) {
		const result = await transform(
			await readFile(`node_modules/${source}`, "utf8"),
			{ minify: true, format: "esm", legalComments: "inline" },
		);
		await writeFile(path, result.code);
	} else {
		await copyFile(`node_modules/${source}`, path);
	}
}
// Alpine's CDN build is an IIFE, not an ES module. Preserve its auto-start behavior.
await copyFile("node_modules/alpinejs/dist/cdn.min.js", "static/js/alpine.min.js");
await copyFile("third_party/alpine/LICENSE.md", "static/js/alpine.LICENSE.md");
console.log("Three.js, Space Grotesk, and Alpine.js assets synchronized.");
