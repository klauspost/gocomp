/*!
 * @kurkle/color v0.6.4
 * https://github.com/kurkle/color#readme
 * (c) 2026 Jukka Kurkela
 * Released under the MIT License
 */

// src/byte.ts
function round(value) {
  return value + 0.5 | 0;
}
var lim = (value, min, max) => Math.max(Math.min(value, max), min);
function p2b(percent) {
  return lim(round(percent * 2.55), 0, 255);
}
function b2p(byte) {
  return lim(round(byte / 2.55), 0, 100);
}
function n2b(ratio) {
  return lim(round(ratio * 255), 0, 255);
}
function b2n(byte) {
  return lim(round(byte / 2.55) / 100, 0, 1);
}
function n2p(ratio) {
  return lim(round(ratio * 100), 0, 100);
}
function parseAlpha(value, percent) {
  if (value === void 0) {
    return 255;
  }
  const v = value === "none" ? 0 : +value;
  return percent ? p2b(v) : n2b(v);
}

// src/hex.ts
var map = {
  0: 0,
  1: 1,
  2: 2,
  3: 3,
  4: 4,
  5: 5,
  6: 6,
  7: 7,
  8: 8,
  9: 9,
  A: 10,
  a: 10,
  B: 11,
  b: 11,
  C: 12,
  c: 12,
  D: 13,
  d: 13,
  E: 14,
  e: 14,
  F: 15,
  f: 15
};
var hex = [..."0123456789ABCDEF"];
var h1 = (b) => hex[b & 15];
var h2 = (b) => hex[(b & 240) >> 4] + hex[b & 15];
var eq = (b) => (b & 240) >> 4 === (b & 15);
var isShort = (v) => eq(v.r) && eq(v.g) && eq(v.b) && eq(v.a);
function hexParse(str) {
  if (str[0] !== "#") {
    return;
  }
  const len = str.length;
  if (len === 4 || len === 5) {
    return {
      a: len === 5 ? map[str[4]] << 4 | map[str[4]] : 255,
      b: map[str[3]] << 4 | map[str[3]],
      g: map[str[2]] << 4 | map[str[2]],
      r: map[str[1]] << 4 | map[str[1]]
    };
  }
  if (len === 7 || len === 9) {
    return {
      a: len === 9 ? map[str[7]] << 4 | map[str[8]] : 255,
      b: map[str[5]] << 4 | map[str[6]],
      g: map[str[3]] << 4 | map[str[4]],
      r: map[str[1]] << 4 | map[str[2]]
    };
  }
}
var alpha = (a, f) => a < 255 ? f(a) : "";
function hexString(color) {
  const f = color && isShort(color) ? h1 : h2;
  return color ? `#${f(color.r)}${f(color.g)}${f(color.b)}${alpha(color.a, f)}` : void 0;
}

// src/hue.ts
var HUE_RE = /^(hsla?|hwb|hsv)\(\s*([-+.e\d]+)(?:deg)?[\s,]+([-+.e\d]+)%[\s,]+([-+.e\d]+)%(?:[\s,]+([-+.e\d]+)(%)?)?\s*\)$/;
function hsl2rgbn(h, s, l) {
  const a = s * Math.min(l, 1 - l);
  const f = (n, k = (n + h / 30) % 12) => l - a * Math.max(Math.min(k - 3, 9 - k, 1), -1);
  return [f(0), f(8), f(4)];
}
function hsv2rgbn(h, s, v) {
  const f = (n, k = (n + h / 60) % 6) => v - v * s * Math.max(Math.min(k, 4 - k, 1), 0);
  return [f(5), f(3), f(1)];
}
function hwb2rgbn(h, w, b) {
  const rgb = hsl2rgbn(h, 1, 0.5);
  let i;
  if (w + b > 1) {
    i = 1 / (w + b);
    w *= i;
    b *= i;
  }
  for (i = 0; i < 3; i++) {
    rgb[i] *= 1 - w - b;
    rgb[i] += w;
  }
  return rgb;
}
function hueValue(r, g, b, d, max) {
  if (r === max) {
    return (g - b) / d + (g < b ? 6 : 0);
  }
  if (g === max) {
    return (b - r) / d + 2;
  }
  return (r - g) / d + 4;
}
function rgb2hsl(color) {
  const range = 255;
  const r = color.r / range;
  const g = color.g / range;
  const b = color.b / range;
  const max = Math.max(r, g, b);
  const min = Math.min(r, g, b);
  const l = (max + min) / 2;
  let h = 0;
  let s = 0;
  let d;
  if (max !== min) {
    d = max - min;
    s = l > 0.5 ? d / (2 - max - min) : d / (max + min);
    h = hueValue(r, g, b, d, max);
    h = h * 60 + 0.5;
  }
  return [h | 0, s || 0, l];
}
function calln(f, a, b, c) {
  return (Array.isArray(a) ? f(a[0], a[1], a[2]) : f(a, b, c)).map(n2b);
}
function hsl2rgb(h, s, l) {
  return calln(hsl2rgbn, h, s, l);
}
function hwb2rgb(h, w, b) {
  return calln(hwb2rgbn, h, w, b);
}
function hsv2rgb(h, s, v) {
  return calln(hsv2rgbn, h, s, v);
}
function hue(h) {
  return (h % 360 + 360) % 360;
}
function hueParse(str) {
  const m = HUE_RE.exec(str);
  let v;
  if (!m) {
    return void 0;
  }
  const a = parseAlpha(m[5], m[6]);
  const h = hue(+m[2]);
  const p1 = +m[3] / 100;
  const p2 = +m[4] / 100;
  if (m[1] === "hwb") {
    v = hwb2rgb(h, p1, p2);
  } else if (m[1] === "hsv") {
    v = hsv2rgb(h, p1, p2);
  } else {
    v = hsl2rgb(h, p1, p2);
  }
  return {
    a,
    b: v[2],
    g: v[1],
    r: v[0]
  };
}
function rotate(color, degrees) {
  const h = rgb2hsl(color);
  const rgb = hsl2rgbn(hue(h[0] + degrees), h[1], h[2]);
  color.r = n2b(rgb[0]);
  color.g = n2b(rgb[1]);
  color.b = n2b(rgb[2]);
}
function hslString(color) {
  if (!color) {
    return void 0;
  }
  const a = rgb2hsl(color);
  const h = a[0];
  const s = n2p(a[1]);
  const l = n2p(a[2]);
  return color.a < 255 ? `hsla(${h}, ${s}%, ${l}%, ${b2n(color.a)})` : `hsl(${h}, ${s}%, ${l}%)`;
}

// src/packed.ts
var names = "alicebluef0f8ff antiquewhitefaebd7 aqua00ffff aquamarine7fffd4 azuref0ffff beigef5f5dc bisqueffe4c4 black000000 blanchedalmondffebcd blue0000ff blueviolet8a2be2 browna52a2a burlywooddeb887 cadetblue5f9ea0 chartreuse7fff00 chocolated2691e coralff7f50 cornflowerblue6495ed cornsilkfff8dc crimsondc143c cyan00ffff darkblue00008b darkcyan008b8b darkgoldenrodb8860b darkgraya9a9a9 darkgreen006400 darkgreya9a9a9 darkkhakibdb76b darkmagenta8b008b darkolivegreen556b2f darkorangeff8c00 darkorchid9932cc darkred8b0000 darksalmone9967a darkseagreen8fbc8f darkslateblue483d8b darkslategray2f4f4f darkslategrey2f4f4f darkturquoise00ced1 darkviolet9400d3 deeppinkff1493 deepskyblue00bfff dimgray696969 dimgrey696969 dodgerblue1e90ff firebrickb22222 floralwhitefffaf0 forestgreen228b22 fuchsiaff00ff gainsborodcdcdc ghostwhitef8f8ff goldffd700 goldenroddaa520 gray808080 green008000 greenyellowadff2f grey808080 honeydewf0fff0 hotpinkff69b4 indianredcd5c5c indigo4b0082 ivoryfffff0 khakif0e68c lavendere6e6fa lavenderblushfff0f5 lawngreen7cfc00 lemonchiffonfffacd lightblueadd8e6 lightcoralf08080 lightcyane0ffff lightgoldenrodyellowfafad2 lightgrayd3d3d3 lightgreen90ee90 lightgreyd3d3d3 lightpinkffb6c1 lightsalmonffa07a lightseagreen20b2aa lightskyblue87cefa lightslategray778899 lightslategrey778899 lightsteelblueb0c4de lightyellowffffe0 lime00ff00 limegreen32cd32 linenfaf0e6 magentaff00ff maroon800000 mediumaquamarine66cdaa mediumblue0000cd mediumorchidba55d3 mediumpurple9370db mediumseagreen3cb371 mediumslateblue7b68ee mediumspringgreen00fa9a mediumturquoise48d1cc mediumvioletredc71585 midnightblue191970 mintcreamf5fffa mistyroseffe4e1 moccasinffe4b5 navajowhiteffdead navy000080 oldlacefdf5e6 olive808000 olivedrab6b8e23 orangeffa500 orangeredff4500 orchidda70d6 palegoldenrodeee8aa palegreen98fb98 paleturquoiseafeeee palevioletreddb7093 papayawhipffefd5 peachpuffffdab9 perucd853f pinkffc0cb plumdda0dd powderblueb0e0e6 purple800080 rebeccapurple663399 redff0000 rosybrownbc8f8f royalblue4169e1 saddlebrown8b4513 salmonfa8072 sandybrownf4a460 seagreen2e8b57 seashellfff5ee siennaa0522d silverc0c0c0 skyblue87ceeb slateblue6a5acd slategray708090 slategrey708090 snowfffafa springgreen00ff7f steelblue4682b4 tand2b48c teal008080 thistled8bfd8 tomatoff6347 turquoise40e0d0 violetee82ee wheatf5deb3 whiteffffff whitesmokef5f5f5 yellowffff00 yellowgreen9acd32";
function unpack() {
  const unpacked = {};
  for (const entry of names.split(" ")) {
    const k = parseInt(entry.slice(-6), 16);
    unpacked[entry.slice(0, -6)] = [k >> 16 & 255, k >> 8 & 255, k & 255];
  }
  return unpacked;
}

// src/names.ts
var names2;
function nameParse(str) {
  if (!names2) {
    names2 = unpack();
    names2.transparent = [0, 0, 0, 0];
  }
  const a = names2[str.toLowerCase()];
  return a && {
    a: a.length === 4 ? a[3] : 255,
    b: a[2],
    g: a[1],
    r: a[0]
  };
}

// src/srgb.ts
function linear2srgb(linear) {
  return linear <= 31308e-7 ? linear * 12.92 : linear ** (1 / 2.4) * 1.055 - 0.055;
}
function srgb2linear(srgb) {
  return srgb <= 0.04045 ? srgb / 12.92 : ((srgb + 0.055) / 1.055) ** 2.4;
}
function interpolate(from, to, t) {
  const r = srgb2linear(b2n(from.r));
  const g = srgb2linear(b2n(from.g));
  const b = srgb2linear(b2n(from.b));
  return {
    a: from.a + t * (to.a - from.a),
    b: n2b(linear2srgb(b + t * (srgb2linear(b2n(to.b)) - b))),
    g: n2b(linear2srgb(g + t * (srgb2linear(b2n(to.g)) - g))),
    r: n2b(linear2srgb(r + t * (srgb2linear(b2n(to.r)) - r)))
  };
}

// src/oklch.ts
var OKLCH_RE = /^oklch\(\s*([-+.e\d]+|none)(%)?[\s,]+([-+.e\d]+|none)(%)?[\s,]+([-+.e\d]+|none)(deg|rad|grad|turn)?(?:[\s,/]+([-+.e\d]+|none)(%)?)?\s*\)$/;
var HUE_UNITS = {
  deg: 1,
  grad: 0.9,
  rad: 180 / Math.PI,
  turn: 360
};
function rgb2oklab(r, g, b) {
  const R = srgb2linear(r / 255);
  const G = srgb2linear(g / 255);
  const B = srgb2linear(b / 255);
  const l = Math.cbrt(0.4122214708 * R + 0.5363325363 * G + 0.0514459929 * B);
  const m = Math.cbrt(0.2119034982 * R + 0.6806995451 * G + 0.1073969566 * B);
  const s = Math.cbrt(0.0883024619 * R + 0.2817188376 * G + 0.6299787005 * B);
  return [
    0.2104542553 * l + 0.793617785 * m - 0.0040720468 * s,
    1.9779984951 * l - 2.428592205 * m + 0.4505937099 * s,
    0.0259040371 * l + 0.7827717662 * m - 0.808675766 * s
  ];
}
function oklab2rgbn(L, a, b) {
  const l0 = L + 0.3963377774 * a + 0.2158037573 * b;
  const m0 = L - 0.1055613458 * a - 0.0638541728 * b;
  const s0 = L - 0.0894841775 * a - 1.291485548 * b;
  const l = l0 * l0 * l0;
  const m = m0 * m0 * m0;
  const s = s0 * s0 * s0;
  return [
    linear2srgb(4.0767416621 * l - 3.3077115913 * m + 0.2309699292 * s),
    linear2srgb(-1.2684380046 * l + 2.6097574011 * m - 0.3413193965 * s),
    linear2srgb(-0.0041960863 * l - 0.7034186147 * m + 1.707614701 * s)
  ];
}
function rgb2oklch(color) {
  const [L, a, b] = rgb2oklab(color.r, color.g, color.b);
  const C = Math.hypot(a, b);
  let H = 0;
  if (C >= 1e-4) {
    H = Math.atan2(b, a) * 180 / Math.PI;
    if (H < 0) {
      H += 360;
    }
  }
  return [L, C, H];
}
function oklch2rgb(l, c, h) {
  let L;
  let C;
  let H;
  if (Array.isArray(l)) {
    L = l[0];
    C = l[1];
    H = l[2];
  } else {
    L = l;
    C = c;
    H = h;
  }
  const rad = H * Math.PI / 180;
  const rgb = oklab2rgbn(L, C * Math.cos(rad), C * Math.sin(rad));
  rgb[0] = n2b(rgb[0]);
  rgb[1] = n2b(rgb[1]);
  rgb[2] = n2b(rgb[2]);
  return rgb;
}
function component(v) {
  return v === "none" ? 0 : +v;
}
function oklchParse(str) {
  const m = OKLCH_RE.exec(str);
  if (!m) {
    return void 0;
  }
  const rawL = component(m[1]);
  const l = lim(m[2] ? rawL / 100 : rawL, 0, 1);
  const rawC = component(m[3]);
  const c = Math.max(m[4] ? rawC / 100 * 0.4 : rawC, 0);
  const h = component(m[5]) * (m[6] ? HUE_UNITS[m[6]] : 1);
  const a = parseAlpha(m[7], !!m[8]);
  const rgb = oklch2rgb(l, c, h);
  return {
    a,
    b: rgb[2],
    g: rgb[1],
    r: rgb[0]
  };
}
function oklchString(color) {
  if (!color) {
    return void 0;
  }
  const [L, C, H] = rgb2oklch(color);
  const l = Math.round(L * 1e4) / 1e4;
  const c = Math.round(C * 1e4) / 1e4;
  const h = Math.round(H * 100) / 100;
  return color.a < 255 ? `oklch(${l} ${c} ${h} / ${b2n(color.a)})` : `oklch(${l} ${c} ${h})`;
}

// src/rgb.ts
var RGB_RE = /^rgba?\(\s*([-+.\d]+)(%)?[\s,]+([-+.e\d]+)(%)?[\s,]+([-+.e\d]+)(%)?(?:[\s,/]+([-+.e\d]+)(%)?)?\s*\)$/;
function rgbParse(str) {
  const m = RGB_RE.exec(str);
  let a = 255;
  let r, g, b;
  if (!m) {
    return void 0;
  }
  if (m[7] !== void 0) {
    const v = +m[7];
    a = m[8] ? p2b(v) : lim(v * 255, 0, 255);
  }
  r = +m[1];
  g = +m[3];
  b = +m[5];
  r = 255 & (m[2] ? p2b(r) : lim(r, 0, 255));
  g = 255 & (m[4] ? p2b(g) : lim(g, 0, 255));
  b = 255 & (m[6] ? p2b(b) : lim(b, 0, 255));
  return {
    a,
    b,
    g,
    r
  };
}
function rgbString(color) {
  return color && (color.a < 255 ? `rgba(${color.r}, ${color.g}, ${color.b}, ${b2n(color.a)})` : `rgb(${color.r}, ${color.g}, ${color.b})`);
}

// src/color.ts
var COMMENT_REGEXP = /\/\*[\s\S]*?\*\//g;
function modHSL(v, i, ratio) {
  if (v) {
    const tmp = rgb2hsl(v);
    tmp[i] = Math.max(0, Math.min(tmp[i] + tmp[i] * ratio, i === 0 ? 360 : 1));
    const rgb = hsl2rgbn(tmp[0], tmp[1], tmp[2]);
    v.r = n2b(rgb[0]);
    v.g = n2b(rgb[1]);
    v.b = n2b(rgb[2]);
  }
}
function clone(v, proto) {
  return v ? Object.assign(proto || {}, v) : v;
}
function fromObject(input) {
  let v = { a: 255, b: 0, g: 0, r: 0 };
  if (Array.isArray(input)) {
    if (input.length >= 3) {
      v = { a: 255, b: input[2], g: input[1], r: input[0] };
      if (input.length > 3) {
        v.a = n2b(input[3]);
      }
    }
  } else {
    v = clone(input, { a: 1, b: 0, g: 0, r: 0 });
    v.a = n2b(v.a);
  }
  return v;
}
function functionParse(str) {
  const clean = (str.indexOf("/*") === -1 ? str : str.replace(COMMENT_REGEXP, "")).trim();
  const first = clean.charAt(0);
  if (first === "r") {
    return rgbParse(clean);
  }
  if (first === "o") {
    return oklchParse(clean);
  }
  return hueParse(clean);
}
var Color = class _Color {
  /**
   * constructor
   * @param input
   */
  constructor(input) {
    if (input instanceof _Color) {
      return input;
    }
    const type = typeof input;
    let v;
    if (type === "object") {
      v = fromObject(input);
    } else if (type === "string") {
      v = hexParse(input) || nameParse(input) || functionParse(input);
    }
    this.h = v;
    this.i = !!v;
  }
  /**
   * `true` if this is a valid color
   * @returns {boolean}
   */
  get valid() {
    return this.i;
  }
  /**
   * @returns {RGBA} - the color
   */
  get rgb() {
    const v = clone(this.h);
    if (v) {
      v.a = b2n(v.a);
    }
    return v;
  }
  /**
   * @param obj - the color
   */
  set rgb(obj) {
    this.h = fromObject(obj);
  }
  /**
   * rgb(a) string
   * @return {string|undefined}
   */
  rgbString() {
    return this.i ? rgbString(this.h) : void 0;
  }
  /**
   * hex string
   * @return {string|undefined}
   */
  hexString() {
    return this.i ? hexString(this.h) : void 0;
  }
  /**
   * hsl(a) string
   * @return {string|undefined}
   */
  hslString() {
    return this.i ? hslString(this.h) : void 0;
  }
  /**
   * oklch string
   * @return {string|undefined}
   */
  oklchString() {
    return this.i ? oklchString(this.h) : void 0;
  }
  /**
   * Mix another color to this color.
   * @param color - Color to mix in
   * @param weight - 0..1
   * @returns {Color}
   */
  mix(color, weight = 0.5) {
    if (color) {
      const v1 = this.h;
      const v2 = color.h;
      const a1 = b2n(v1.a);
      const a2 = b2n(v2.a);
      const w = 2 * weight - 1;
      const a = a1 - a2;
      const w1 = ((w * a === -1 ? w : (w + a) / (1 + w * a)) + 1) / 2;
      const w2 = 1 - w1;
      v1.r = 255 & w1 * v1.r + w2 * v2.r + 0.5;
      v1.g = 255 & w1 * v1.g + w2 * v2.g + 0.5;
      v1.b = 255 & w1 * v1.b + w2 * v2.b + 0.5;
      v1.a = n2b(weight * a1 + (1 - weight) * a2);
    }
    return this;
  }
  /**
   * Interpolate a color value between this and `color`
   * @param color
   * @param t - 0..1
   * @returns {Color}
   */
  interpolate(color, t) {
    if (color) {
      this.h = interpolate(this.h, color.h, t);
    }
    return this;
  }
  /**
   * Clone
   * @returns {Color}
   */
  clone() {
    return new _Color(this.rgb);
  }
  /**
   * Set alpha
   * @param a - the alpha [0..1]
   * @returns {Color}
   */
  alpha(a) {
    this.h.a = n2b(a);
    return this;
  }
  /**
   * Make clearer
   * @param ratio - ratio [0..1]
   * @returns {Color}
   */
  clearer(ratio) {
    const rgb = this.h;
    rgb.a *= 1 - ratio;
    return this;
  }
  /**
   * Convert to grayscale
   * @returns {Color}
   */
  greyscale() {
    const rgb = this.h;
    const val = round(rgb.r * 0.3 + rgb.g * 0.59 + rgb.b * 0.11);
    rgb.r = rgb.g = rgb.b = val;
    return this;
  }
  /**
   * Opaquer
   * @param ratio - ratio [0..1]
   * @returns {Color}
   */
  opaquer(ratio) {
    const rgb = this.h;
    rgb.a *= 1 + ratio;
    return this;
  }
  /**
   * Negates the rgb value
   * @returns {Color}
   */
  negate() {
    const v = this.h;
    v.r = 255 - v.r;
    v.g = 255 - v.g;
    v.b = 255 - v.b;
    return this;
  }
  /**
   * Lighten
   * @param ratio - ratio [0..1]
   * @returns {Color}
   */
  lighten(ratio) {
    modHSL(this.h, 2, ratio);
    return this;
  }
  /**
   * Darken
   * @param ratio - ratio [0..1]
   * @returns {Color}
   */
  darken(ratio) {
    modHSL(this.h, 2, -ratio);
    return this;
  }
  /**
   * Saturate
   * @param ratio - ratio [0..1]
   * @returns {Color}
   */
  saturate(ratio) {
    modHSL(this.h, 1, ratio);
    return this;
  }
  /**
   * Desaturate
   * @param ratio - ratio [0..1]
   * @returns {Color}
   */
  desaturate(ratio) {
    modHSL(this.h, 1, -ratio);
    return this;
  }
  /**
   * Rotate
   * @param deg - degrees to rotate
   * @returns {Color}
   */
  rotate(deg) {
    rotate(this.h, deg);
    return this;
  }
};

// src/index.esm.ts
function index_esm_default(input) {
  return new Color(input);
}
export {
  Color,
  b2n,
  b2p,
  index_esm_default as default,
  hexParse,
  hexString,
  hsl2rgb,
  hsl2rgbn,
  hslString,
  hsv2rgb,
  hueParse,
  hwb2rgb,
  interpolate,
  lim,
  linear2srgb,
  n2b,
  n2p,
  nameParse,
  oklch2rgb,
  oklchParse,
  oklchString,
  p2b,
  parseAlpha,
  rgb2hsl,
  rgb2oklch,
  rgbParse,
  rgbString,
  rotate,
  round,
  srgb2linear
};
//# sourceMappingURL=color.esm.js.map
