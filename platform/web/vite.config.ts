import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
export default defineConfig({plugins:[vue()],build:{manifest:true},server:{allowedHosts:['school.localhost','.school.localhost'],proxy:{'/api':{target:'http://127.0.0.1:8080',changeOrigin:false},'/files':{target:'http://127.0.0.1:8080',changeOrigin:false}}}})
