<script setup lang="ts">
import { computed, ref } from 'vue'
import { vLazyLoad } from '@/directives/lazyLoad'

interface Props {
  id: number
  photoId?: number
  type: 'account' | 'channel'
  name: string
  size?: number
}

const props = withDefaults(defineProps<Props>(), {
  size: 40
})

const imageError = ref(false)

const avatarUrl = computed(() => {
  if (!props.photoId || props.photoId <= 0) return ''
  return `/api/${props.type === 'account' ? 'accounts' : 'channels'}/${props.id}/avatar`
})

const showImage = computed(() => avatarUrl.value && !imageError.value)

const initials = computed(() => {
  const words = props.name.trim().split(/\s+/)
  if (words.length >= 2) {
    return (words[0][0] + words[words.length - 1][0]).toUpperCase()
  }
  return props.name.substring(0, 2).toUpperCase()
})

const backgroundColor = computed(() => {
  // Generate consistent color based on name
  let hash = 0
  for (let i = 0; i < props.name.length; i++) {
    hash = props.name.charCodeAt(i) + ((hash << 5) - hash)
  }
  const hue = Math.abs(hash % 360)
  return `hsl(${hue}, 60%, 65%)`
})

function onImageError() {
  imageError.value = true
}
</script>

<template>
  <div
    class="avatar"
    :style="{
      width: `${size}px`,
      height: `${size}px`,
      fontSize: `${size * 0.4}px`,
      backgroundColor: showImage ? 'transparent' : backgroundColor
    }"
  >
    <img
      v-if="showImage"
      v-lazy-load
      :data-src="avatarUrl"
      :alt="name"
      class="avatar-image"
      @error="onImageError"
    >
    <span v-else class="avatar-initials">{{ initials }}</span>
  </div>
</template>

<style scoped>
.avatar {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border-radius: 50%;
  overflow: hidden;
  flex-shrink: 0;
  color: white;
  font-weight: 500;
  user-select: none;
}

.avatar-image {
  width: 100%;
  height: 100%;
  object-fit: cover;
}

.avatar-initials {
  line-height: 1;
}
</style>
