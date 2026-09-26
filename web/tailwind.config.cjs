/** @type {import('tailwindcss').Config} */
module.exports = {
  content: ['./index.html', './src/**/*.{ts,tsx}'],
  theme: {
    extend: {
      colors: {
        paper: '#f7f8f5',
        ink: '#202b26',
        muted: '#5e6c62',
        line: '#dfe5df',
        moss: '#24664d',
      },
    },
  },
  plugins: [],
}
