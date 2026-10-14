import type { Scenario } from "@/events/schema";

/** Colour swatch + two-letter code: the scenario identity (never colour alone). */
export function ScenarioBadge({ code, scenarios, title }: { code?: string; scenarios: Scenario[]; title?: boolean }) {
  if (!code) return null;
  const s = scenarios.find((x) => x.code === code);
  return (
    <span className="inline-flex items-center gap-1 whitespace-nowrap rounded px-1 text-[11px] font-bold" title={s?.title}>
      <span className="inline-block h-2.5 w-2.5 rounded-sm" style={{ background: s?.color ?? "#888" }} aria-hidden />
      {code.toUpperCase()}
      {title && s ? <span className="font-normal text-ink-2">{s.title}</span> : null}
    </span>
  );
}
