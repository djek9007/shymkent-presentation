import { defineConfig, loadEnv } from 'vite'
import vue from '@vitejs/plugin-vue'
export default defineConfig(({mode})=>{const env=loadEnv(mode,'.','DEV_');return {plugins:[vue()],build:{manifest:true},server:{allowedHosts:['school.localhost','.school.localhost'],proxy:{'/api':{target:env.DEV_API_TARGET||'http://127.0.0.1:8080',changeOrigin:false},'/files':{target:env.DEV_API_TARGET||'http://127.0.0.1:8080',changeOrigin:false}}}}})
