/** @type {import('tailwindcss').Config} */
export default {
  content: [
    "./index.html",
    "./src/**/*.{js,ts,jsx,tsx}",
    "./templates/**/*.{html,tmpl}",
    "./public/**/*.html"
  ],
  darkMode: 'class',
  theme: {
    extend: {
      fontFamily: {
        'serif': ['Instrument Serif', 'serif'],
        'sans': ['Inter', 'sans-serif'],
      },
      colors: {
        'base-light': '#eeede8',
        'base-medium': '#f7f7f5',
        'gray-medium': '#666',
        'base': '#eeede8',
      },
      typography: {
        DEFAULT: {
          css: {
            'max-width': 'none',
          },
        },
      },
    }
  },
  plugins: [
    require('@tailwindcss/typography'),
  ],
}