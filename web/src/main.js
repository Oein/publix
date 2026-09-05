import { mount } from 'svelte';
import './app.css';
import App from './App.svelte';
import { initTheme } from './lib/theme.svelte.js';
import { initLocale } from './lib/i18n.svelte.js';

// Before the first render: a dashboard that paints light and then flips to
// dark, or English and then Korean, looks broken even though it is right a
// frame later.
initTheme();
initLocale();

export default mount(App, { target: document.getElementById('app') });
