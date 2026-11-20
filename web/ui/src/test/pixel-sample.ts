import type { AvatarDoc, AvatarParts, Sprite } from "../lib/pixel-avatar";

/* Test sprites on the spec 62 template: a head with two dot eyes and hair, a
   chin line, a shirt in `shirt`, hands, pants, and shoes. */
export function sampleSprite(shirt = "#7c5cff", hair = "#4d3a33"): Sprite {
  const head = Array.from({ length: 20 }, () => Array<string>(16).fill("."));
  const body = Array.from({ length: 20 }, () => Array<string>(16).fill("."));
  const put = (layer: string[][], x: number, y: number, key: string) => {
    const row = layer[y];
    if (row) {
      row[x] = key;
    }
  };
  for (let y = 3; y <= 11; y++) {
    const [x0, x1] = y === 3 || y === 11 ? [4, 11] : [3, 12];
    for (let x = x0; x <= x1; x++) {
      put(head, x, y, x <= 4 ? "S" : "s");
    }
  }
  for (let x = 5; x <= 10; x++) {
    put(head, x, 12, "k");
  }
  for (const x of [7, 11]) {
    put(head, x, 7, "e");
    put(head, x, 8, "e");
  }
  for (let y = 2; y <= 4; y++) {
    for (let x = 3; x <= 12; x++) {
      put(head, x, y, "h");
    }
  }
  for (let y = 13; y <= 14; y++) {
    for (let x = 5; x <= 10; x++) {
      put(body, x, y, "c");
    }
  }
  put(body, 4, 13, "c");
  put(body, 11, 13, "c");
  put(body, 4, 14, "s");
  put(body, 11, 14, "s");
  for (let x = 5; x <= 10; x++) {
    put(body, x, 15, "p");
  }
  for (const x of [5, 6, 9, 10]) {
    put(body, x, 16, "p");
    put(body, x, 17, "o");
  }
  return {
    palette: {
      k: "#1b1820",
      s: "#f1c3a2",
      S: "#d69a78",
      e: "#1b1820",
      h: hair,
      c: shirt,
      p: "#3f5d8f",
      o: "#2a2630",
    },
    head: head.map((row) => row.join("")),
    body: body.map((row) => row.join("")),
  };
}

export const SAMPLE_PARTS: AvatarParts = {
  species: "human",
  hair: "short",
  hair_color: "dark",
  skin: "light",
  eyes: "dot",
  accessory: "none",
  outfit: "plain",
  pants: "denim",
  blush: true,
  color: "brand",
  accessory_color: "#f45bd0",
};

export function sampleAvatar(shirt = "#7c5cff", hair = "#4d3a33"): AvatarDoc {
  return { kind: "parts", parts: { ...SAMPLE_PARTS }, sprite: sampleSprite(shirt, hair) };
}

export function sampleDrawn(): AvatarDoc {
  return { kind: "drawn", sprite: sampleSprite("#13b5c8", "#e3683f"), prompt: "a teal wizard" };
}
