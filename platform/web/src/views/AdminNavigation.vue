<script setup lang="ts">
import { t } from '../api'
defineProps<{tab:string;central:boolean;available:string[][]}>()
defineEmits<{navigate:[value:string]}>()
</script>
<template>
<nav class="admin-navigation" :aria-label="t('Панель бөлімдері','Разделы панели')">
  <button :aria-current="tab==='menu'?'page':undefined" @click="$emit('navigate','menu')">{{t('Мәзір','Меню сайта')}}</button>
  <button v-if="central" :aria-current="tab==='users'?'page':undefined" @click="$emit('navigate','users')">{{t('Пайдаланушылар','Пользователи')}}</button>
  <button v-if="central" :aria-current="tab==='organizations'?'page':undefined" @click="$emit('navigate','organizations')">{{t('Ұйымдар','Организации')}}</button>
  <p class="admin-nav-label">{{t('Материалдар','Материалы')}}</p>
  <button v-for="s in available" :key="s[0]" :aria-current="tab===s[0]?'page':undefined" @click="$emit('navigate',s[0])">{{t(s[1],s[2])}}</button>
</nav>
</template>
