
export function FilterSelect({
  label,
  value,
  onChange,
  children,
  title,
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  children: React.ReactNode;
  title?: string;
}) {
  return (
    <div className="space-y-1">
      <label className="text-[10px] uppercase tracking-wide text-text-secondary">
        {label}
      </label>
      <select
        className="w-full text-xs rounded bg-surface-2 border border-border px-2 py-1.5 text-text-primary"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        title={title}
      >
        {children}
      </select>
    </div>
  );
}
