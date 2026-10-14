/**
 * WhatsApp 号码的小工具（#116）。
 *
 * 用户在印尼，习惯写「0812-3456-7890」「62 812 3456 7890」，服务端只收 E.164（`+6281234567890`），
 * 所以前端先规范一遍；认不出来就当不合法，不替用户乱猜国家。最终以服务端校验为准。
 */

const E164 = /^\+[1-9]\d{7,14}$/;

/** 规范成 E.164；认不出来返回 null。 */
export function normalizeWaPhone(input: string): string | null {
  let s = input.trim().replace(/[\s\-().]/g, "");
  if (s.startsWith("00")) s = "+" + s.slice(2);
  else if (s.startsWith("0")) s = "+62" + s.slice(1); // 印尼本地写法
  else if (s.startsWith("62")) s = "+" + s;
  else if (s.startsWith("8")) s = "+62" + s; // 只写了「812…」
  return E164.test(s) ? s : null;
}

/** 页面上显示的脱敏号码：「+62 812****890」。 */
export function maskWaPhone(e164: string): string {
  if (e164.startsWith("+62")) {
    const national = e164.slice(3);
    return `+62 ${national.slice(0, 3)}****${national.slice(-3)}`;
  }
  return `${e164.slice(0, 4)}****${e164.slice(-3)}`;
}
