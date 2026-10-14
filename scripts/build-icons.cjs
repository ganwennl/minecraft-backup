// Render the editable SVG sources into native Windows multi-resolution icons.
// Normal application builds use the checked-in ICOs and need no Node dependency.
const fs = require('node:fs');
const path = require('node:path');

let sharp;
try { sharp = require('sharp'); }
catch {
  const bundled = path.join(process.env.USERPROFILE || '', '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/sharp');
  sharp = require(bundled);
}
const root = path.resolve(__dirname, '..');
const sizes = [16, 20, 24, 32, 40, 48, 64, 96, 128, 256];
const names = ['app', 'backup', 'restore', 'folder', 'file', 'refresh', 'exit'];

function dib(rgba, size) {
  const stride = Math.ceil(size / 32) * 4;
  const bytes = Buffer.alloc(40 + size * size * 4 + stride * size);
  bytes.writeUInt32LE(40, 0);
  bytes.writeInt32LE(size, 4);
  bytes.writeInt32LE(size * 2, 8); // XOR bitmap plus the monochrome AND mask.
  bytes.writeUInt16LE(1, 12);
  bytes.writeUInt16LE(32, 14);
  bytes.writeUInt32LE(size * size * 4, 20);
  for (let y = 0; y < size; y++) {
    for (let x = 0; x < size; x++) {
      const src = (y * size + x) * 4;
      const dst = 40 + ((size - y - 1) * size + x) * 4;
      bytes[dst] = rgba[src + 2];
      bytes[dst + 1] = rgba[src + 1];
      bytes[dst + 2] = rgba[src];
      bytes[dst + 3] = rgba[src + 3];
      if (rgba[src + 3] === 0) {
        const mask = 40 + size * size * 4 + (size - y - 1) * stride + Math.floor(x / 8);
        bytes[mask] |= 0x80 >> (x % 8);
      }
    }
  }
  return bytes;
}

async function render(source, size) {
  if (size <= 24) source = source.replace(/<!--detail-start-->[\s\S]*?<!--detail-end-->/, '');
  // Rasterize each size from vector geometry, rather than reducing a large PNG.
  return sharp(Buffer.from(source)).resize(size, size).ensureAlpha().raw().toBuffer();
}

async function main() {
  const preview = [];
  for (const name of names) {
    const source = fs.readFileSync(path.join(root, 'assets', name + '.svg'), 'utf8');
    const frames = [];
    for (const size of sizes) {
      const rgba = await render(source, size);
      frames.push(size === 256 ? await sharp(rgba, { raw: { width: size, height: size, channels: 4 } }).png().toBuffer() : dib(rgba, size));
    }
    const header = Buffer.alloc(6 + sizes.length * 16);
    header.writeUInt16LE(1, 2);
    header.writeUInt16LE(sizes.length, 4);
    let offset = header.length;
    frames.forEach((data, i) => {
      const entry = 6 + i * 16;
      header[entry] = sizes[i] % 256;
      header[entry + 1] = sizes[i] % 256;
      header.writeUInt16LE(1, entry + 4);
      header.writeUInt16LE(32, entry + 6);
      header.writeUInt32LE(data.length, entry + 8);
      header.writeUInt32LE(offset, entry + 12);
      offset += data.length;
    });
    fs.writeFileSync(path.join(root, 'assets', name + '.ico'), Buffer.concat([header, ...frames]));
    const rgba = await render(source, 96);
    preview.push({ input: await sharp(rgba, { raw: { width: 96, height: 96, channels: 4 } }).png().toBuffer(), left: 24 + names.indexOf(name) * 136, top: 24 });
  }
  fs.mkdirSync(path.join(root, 'build'), { recursive: true });
  await sharp({ create: { width: 976, height: 144, channels: 4, background: '#f1f5f2' } }).composite(preview).png().toFile(path.join(root, 'build', 'icons-preview.png'));
  const app = fs.readFileSync(path.join(root, 'assets', 'app.svg'), 'utf8');
  const tiles = [];
  let left = 20;
  for (const size of sizes) {
    tiles.push({ input: await sharp(await render(app, size), { raw: { width: size, height: size, channels: 4 } }).png().toBuffer(), left, top: 20 });
    left += size + 20;
  }
  await sharp({ create: { width: left, height: 296, channels: 4, background: '#f1f5f2' } }).composite(tiles).png().toFile(path.join(root, 'build', 'app-sizes-preview.png'));
  process.stdout.write(`Generated ${names.length} icons, ${sizes.join('/')} px\n`);
}
main().catch(error => { process.stderr.write(error.stack + '\n'); process.exitCode = 1; });
