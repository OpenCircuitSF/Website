import { mount } from 'svelte';
import './app.css';
import App from './App.svelte';
import { prepareMountTarget } from './lib/mountTarget';
import { get } from 'svelte/store';
import { currentRoute } from './lib/router';
import { preloadForRoute } from './lib/routeViews';

// Svelte 5 mounts the root component imperatively. The #app element is
// defined in index.html, and (#0519) may already hold server-rendered
// fallback content for this route -- prepareMountTarget clears it before
// mount() runs, since mount() APPENDS rather than replaces. See that
// function's own doc comment.
//
// #0717: a lazily loaded route's chunk is awaited first, so a direct load or
// a reload keeps the server fallback on screen instead of a blank shell. A
// failed chunk has already started the once-per-URL reload; if that was
// spent, mount anyway and LazyView shows its failure state.
void preloadForRoute(get(currentRoute)).finally(() => {
  mount(App, { target: prepareMountTarget() });
});
