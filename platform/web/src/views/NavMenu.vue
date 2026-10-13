<script setup lang="ts">
import {lang,type MenuNode} from '../api'
defineProps<{items:MenuNode[];activeKey:string;level?:number}>()
function active(item:MenuNode,key:string):boolean{return item.key===key||item.children.some(child=>active(child,key))}

</script>
<template><ul class="nav-list" :class="{'nav-submenu':(level||0)>0}"><li v-for="item in items" :key="item.key"><details v-if="item.group" class="nav-group" :class="{'nav-group-active':active(item,activeKey)}"><summary>{{item.label[lang]}}<span class="nav-chevron" aria-hidden="true">⌄</span></summary><NavMenu :items="item.children" :active-key="activeKey" :level="(level||0)+1" /></details><a v-else-if="item.url && /^https?:\/\//i.test(item.url)" :href="item.url">{{item.label[lang]}}</a><RouterLink v-else :to="item.url||(item.key==='home'?'/':'/'+item.key)" :class="{'nav-leaf-active':item.key===activeKey}">{{item.label[lang]}}</RouterLink></li></ul></template>
