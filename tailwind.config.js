/** @type {import('tailwindcss').Config} */
module.exports = {
  content: ['./backend-go/cmd/web/**/*.go', './backend-go/internal/web/**/*.go', './frontend/templates/**/*.html'],
  theme: {
    extend: {
      colors: { accent: {50:'#eef2f6',100:'#dbe4ec',200:'#b9cbdb',300:'#93b0c8',400:'#7699b7',500:'#5980a6',600:'#4a6c8c',700:'#3c5771',800:'#2e4258',900:'#212f3f'} },
      fontFamily: { heading: ['Barlow Condensed','sans-serif'], body: ['Barlow','system-ui','sans-serif'] }
    }
  },
  plugins: [],
};
