<script setup lang="ts">
defineProps<{ size?: number }>()
</script>

<template>
  <svg
    :width="size ?? 18"
    :height="size ?? 18"
    viewBox="5 1 130 122"
    aria-hidden="true"
    xmlns="http://www.w3.org/2000/svg"
  >
    <defs>
      <linearGradient id="bm-top" x1="120" y1="20" x2="55" y2="80" gradientUnits="userSpaceOnUse">
        <stop offset="0" stop-color="#f4f7f7" />
        <stop offset="0.5" stop-color="#e6ecec" />
        <stop offset="1" stop-color="#d6dddd" />
      </linearGradient>
      <linearGradient id="bm-right" x1="115" y1="18" x2="115" y2="95" gradientUnits="userSpaceOnUse">
        <stop offset="0" stop-color="#f4f2fc" />
        <stop offset="0.30" stop-color="#dad7f4" />
        <stop offset="0.60" stop-color="#acaeea" />
        <stop offset="0.85" stop-color="#8087d4" />
        <stop offset="1" stop-color="#6a72c5" />
      </linearGradient>
      <linearGradient id="bm-bottom" x1="28" y1="101" x2="110" y2="115" gradientUnits="userSpaceOnUse">
        <stop offset="0" stop-color="#4a4a7d" />
        <stop offset="0.25" stop-color="#6c66a3" />
        <stop offset="0.55" stop-color="#a89ed4" />
        <stop offset="0.80" stop-color="#d6cef0" />
        <stop offset="1" stop-color="#ebe6f8" />
      </linearGradient>
      <linearGradient id="bm-hull" x1="0" y1="0" x2="0" y2="1">
        <stop offset="0" stop-color="#1f2638" />
        <stop offset="1" stop-color="#161b29" />
      </linearGradient>
    </defs>
    <!-- Hull (dark base) -->
    <polygon points="133,4 127,120 8,96" fill="url(#bm-hull)" stroke="url(#bm-hull)" stroke-width="6" stroke-linejoin="round" stroke-linecap="round" />
    <!-- Top facet (light) -->
    <polygon points="130,7 99,74 46,84" fill="url(#bm-top)" stroke="#0e1320" stroke-width="0.35" stroke-linejoin="round" />
    <!-- Right facet (purple gradient) -->
    <polygon points="130,7 99,74 115,95" fill="url(#bm-right)" stroke="#0e1320" stroke-width="0.35" stroke-linejoin="round" />
    <!-- Inner dark facet -->
    <polygon points="99,74 115,95 123,117 68,81" fill="#1a2033" stroke="#0e1320" stroke-width="0.35" stroke-linejoin="round" />
    <!-- Bottom facet (bottom left, lighter purple) -->
    <polygon points="68,81 123,117 28,101 46,84" fill="url(#bm-bottom)" stroke="#0e1320" stroke-width="0.35" stroke-linejoin="round" />
    <!-- Edge highlights -->
    <path d="M 46 84 L 99 74" stroke="#ffffff" stroke-opacity="0.25" stroke-width="0.5" fill="none" />
    <path d="M 99 74 L 130 7" stroke="#ffffff" stroke-opacity="0.18" stroke-width="0.5" fill="none" />
  </svg>
</template>
