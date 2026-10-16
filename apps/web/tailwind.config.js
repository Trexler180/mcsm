/** @type {import('tailwindcss').Config} */
export default {
  content: ['./index.html', './src/**/*.{ts,tsx}'],
  theme: {
    extend: {
      screens: {
        // Touch-screen devices, regardless of viewport width.
        touch: { raw: '(pointer: coarse)' },
      },
      // Older screens' color names, mapped onto the new palette per theme
      // in src/ui/styles/legacy.css.
      colors: {
        background: 'rgb(var(--legacy-background) / <alpha-value>)',
        surface: 'rgb(var(--legacy-surface) / <alpha-value>)',
        'surface-2': 'rgb(var(--legacy-surface-2) / <alpha-value>)',
        border: 'rgb(var(--legacy-border) / <alpha-value>)',
        'border-hover': 'rgb(var(--legacy-border-hover) / <alpha-value>)',
        accent: {
          DEFAULT: 'rgb(var(--legacy-accent) / <alpha-value>)',
          hover: 'rgb(var(--legacy-accent-hover) / <alpha-value>)',
          muted: 'rgb(var(--legacy-accent-muted) / <alpha-value>)',
        },
        muted: 'rgb(var(--legacy-muted) / <alpha-value>)',
        'text-primary': 'rgb(var(--legacy-text-primary) / <alpha-value>)',
        'text-secondary': 'rgb(var(--legacy-text-secondary) / <alpha-value>)',
      },
      fontFamily: {
        mono: [
          'Geist Mono Variable',
          'JetBrains Mono Variable',
          'Fira Code',
          'Consolas',
          'monospace',
        ],
      },
    },
  },
  // The new design system owns these class names (src/ui/styles/base.css).
  blocklist: ['ring'],
  plugins: [],
}
