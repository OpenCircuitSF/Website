import { mount } from 'svelte';
import './app.css';
import App from './App.svelte';
import { prepareMountTarget } from './lib/mountTarget';

// Svelte 5 mounts the root component imperatively. The #app element is
// defined in index.html, and (#0519) may already hold server-rendered
// fallback content for this route -- prepareMountTarget clears it before
// mount() runs, since mount() APPENDS rather than replaces. See that
// function's own doc comment.
const app = mount(App, {
  target: prepareMountTarget(),
});

export default app;
