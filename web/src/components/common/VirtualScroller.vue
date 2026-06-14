<script setup lang="ts" generic="T">
import { computed, ref } from 'vue'

interface Props<T> {
  items: T[]
  itemHeight: number
  bufferSize?: number
}

const props = withDefaults(defineProps<Props<T>>(), {
  bufferSize: 5
})

const scrollTop = ref(0)
const containerHeight = ref(800) // Default, will measure on mount

const totalHeight = computed(() => props.items.length * props.itemHeight)

const visibleStart = computed(() =>
  Math.max(0, Math.floor(scrollTop.value / props.itemHeight) - props.bufferSize)
)

const visibleEnd = computed(() =>
  Math.min(
    props.items.length,
    Math.ceil((scrollTop.value + containerHeight.value) / props.itemHeight) + props.bufferSize
  )
)

const visibleItems = computed(() =>
  props.items.slice(visibleStart.value, visibleEnd.value)
)

const offsetY = computed(() => visibleStart.value * props.itemHeight)

function handleScroll(event: Event) {
  scrollTop.value = (event.target as HTMLElement).scrollTop
}
</script>

<template>
  <div class="virtual-scroller" @scroll="handleScroll">
    <div
      class="virtual-content"
      :style="{
        height: totalHeight + 'px',
        paddingTop: offsetY + 'px'
      }"
    >
      <div
        v-for="(item, idx) in visibleItems"
        :key="(item as any).id ?? visibleStart + idx"
        :data-index="visibleStart + idx"
      >
        <slot name="item" :item="item" :index="visibleStart + idx" />
      </div>
    </div>
  </div>
</template>

<style scoped>
.virtual-scroller {
  overflow-y: auto;
  overflow-x: hidden;
  height: 100%;
}

.virtual-content {
  position: relative;
}
</style>
