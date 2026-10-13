import { createApp } from 'vue'
import { createRouter, createWebHistory } from 'vue-router'
import App from './App.vue'
import './style.css'
const router=createRouter({history:createWebHistory(),routes:[{path:'/admin/:pathMatch(.*)*',component:()=>import('./views/Admin.vue')},{path:'/:pathMatch(.*)*',component:()=>import('./views/Public.vue')}]})
createApp(App).use(router).mount('#app')
