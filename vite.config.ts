import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
export default defineConfig({plugins:[vue()],root:'apps/ui',build:{outDir:'../../internal/webui/dist',emptyOutDir:true},server:{proxy:{'/netgo':'http://127.0.0.1:8081'}}})
