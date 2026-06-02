/** @type {import('tailwindcss').Config} */
export default {
  content: ['./index.html', './src/**/*.{ts,tsx}'],
  theme: {
    extend: {
      colors: {
        background: '#0f0f0f',
        surface: '#1a1a1a',
        'surface-2': '#242424',
        border: '#2a2a2a',
        'border-hover': '#3a3a3a',
        accent: {
          DEFAULT: '#22c55e',
          hover: '#16a34a',
          muted: '#14532d',
        },
        muted: '#6b7280',
        'text-primary': '#f9fafb',
        'text-secondary': '#9ca3af',
      },
      fontFamily: {
        mono: ['JetBrains Mono', 'Fira Code', 'Consolas', 'monospace'],
      },
    },
  },
  plugins: [],
}
