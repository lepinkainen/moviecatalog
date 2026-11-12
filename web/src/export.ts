import { mount } from 'svelte';
import ExportApp from './ExportApp.svelte';
import './app.css';

mount(ExportApp, { target: document.getElementById('app')! });
