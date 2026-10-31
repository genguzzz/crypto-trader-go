// Generative-UI blocks in the agent's Markdown (port of web/src/components/ai/blocks.ts): a fenced
// block whose info string is a block type and whose body is JSON (```plan {...}```) renders as a
// component; everything else stays Markdown. While streaming, an unclosed block is `open`
// (skeleton); a closed block with bad JSON falls back to its raw text. Schemas: ai/prompts/chat_v1.md.
export const BLOCK_TYPES = ["verdict", "kpi", "plan", "coins", "bullbear"] as const;
export type BlockType = (typeof BLOCK_TYPES)[number];

export type Segment =
  | { k: "md"; text: string; key: string }
  | { k: "block"; type: BlockType; data: any; open: boolean; raw: string; error: boolean; key: string };

const FENCE = /^```([a-z]+)\s*$/;

export function segments(src: string, live: boolean): Segment[] {
  const out: Segment[] = [];
  const lines = src.split("\n");
  let md: string[] = [];
  const flush = () => {
    if (md.join("").trim()) out.push({ k: "md", text: md.join("\n"), key: `m${out.length}` });
    md = [];
  };
  for (let i = 0; i < lines.length; i++) {
    const m = FENCE.exec(lines[i].trim());
    if (!m || !(BLOCK_TYPES as readonly string[]).includes(m[1])) {
      md.push(lines[i]);
      continue;
    }
    let j = i + 1;
    while (j < lines.length && lines[j].trim() !== "```") j++;
    const closed = j < lines.length;
    const raw = lines.slice(i + 1, j).join("\n");
    flush();
    let data: any = null;
    let error = false;
    if (closed || !live) {
      try {
        data = JSON.parse(raw);
      } catch {
        error = true;
      }
    }
    out.push({ k: "block", type: m[1] as BlockType, data, open: !closed && live, raw, error, key: `b${out.length}` });
    i = closed ? j : lines.length;
  }
  flush();
  return out;
}

const INST = /^[A-Z0-9]{1,20}-USDT-SWAP$/;
/** Only well-formed perpetual ids become in-app links. */
export const instOk = (id: unknown): id is string => typeof id === "string" && INST.test(id);
export const symOf = (id: string) => id.split("-")[0];
export const num = (v: unknown): number | null =>
  typeof v === "number" && Number.isFinite(v) ? v : typeof v === "string" && v.trim() !== "" && Number.isFinite(+v) ? +v : null;
