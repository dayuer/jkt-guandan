/**
 * WhatsApp 号码的小工具（#116）。
 *
 * 用户在印尼，习惯写「0812-3456-7890」「62 812 3456 7890」，服务端只收 E.164（`+6281234567890`），
 * 所以前端先规范一遍；认不出来就当不合法，不替用户乱猜国家。最终以服务端校验为准。
 */

const E164 = /^\+[1-9]\d{7,14}$/;

/**
 * 规范成 E.164；认不出来、或者猜不准是哪个国家，返回 null（页面会让用户加上国家码）。
 *
 * 只在没有歧义时补 +62：
 *   - 带「+」或「00」的：国家码是用户自己写的，原样用；
 *   - 「0」开头：印尼本地写法（0812…），去掉 0 补 +62；
 *   - 「62」开头、后面是 8 开头的 9–12 位：印尼号码少写了「+」；
 *   - 「8」开头的 9–12 位裸号：印尼手机号省了开头的 0（812…）；
 *   - 「86」开头的 13 位：中国手机号少写了「+」（86 138 1234 5678）。
 * 其他「86」开头的裸号既可能是 +86 也可能是印尼号，不猜（#125 复审）。
 */
export function normalizeWaPhone(input: string): string | null {
  const s = input.trim().replace(/[\s\-().]/g, "");
  let out: string | null = null;
  if (s.startsWith("+")) out = s;
  else if (s.startsWith("00")) out = "+" + s.slice(2);
  else if (!/^\d+$/.test(s)) out = null;
  else if (s.startsWith("0")) out = "+62" + s.slice(1);
  else if (/^628\d{8,11}$/.test(s)) out = "+" + s;
  else if (/^86\d{11}$/.test(s)) out = "+" + s;
  else if (/^8\d{8,11}$/.test(s) && !s.startsWith("86")) out = "+62" + s;
  return out && E164.test(out) ? out : null;
}

/** 页面上显示的脱敏号码：「+62 812****890」。 */
export function maskWaPhone(e164: string): string {
  if (e164.startsWith("+62")) {
    const national = e164.slice(3);
    return `+62 ${national.slice(0, 3)}****${national.slice(-3)}`;
  }
  return `${e164.slice(0, 4)}****${e164.slice(-3)}`;
}
