<script setup lang="ts">
defineProps<{ name: string; size?: number }>()
</script>

<template>
  <svg
    :width="size ?? 14"
    :height="size ?? 14"
    viewBox="0 0 16 16"
    fill="none"
    stroke="currentColor"
    stroke-width="1.25"
    stroke-linecap="round"
    stroke-linejoin="round"
    aria-hidden="true"
  >
    <path v-if="name === 'check'" d="M3 8.5l3 3 7-7" />
    <path v-else-if="name === 'chevron-down'" d="M4 6l4 4 4-4" />
    <path v-else-if="name === 'chevron-right'" d="M6 4l4 4-4 4" />
    <path v-else-if="name === 'chevron-left'" d="M10 4l-4 4 4 4" />
    <path v-else-if="name === 'chevrons-left'" d="M9 4L5 8l4 4M13 4L9 8l4 4" />
    <path v-else-if="name === 'braces'" d="M5 3H4a1 1 0 0 0-1 1v3l-1 1 1 1v3a1 1 0 0 0 1 1h1M11 3h1a1 1 0 0 1 1 1v3l1 1-1 1v3a1 1 0 0 1-1 1h-1" />
    <path v-else-if="name === 'chevrons-right'" d="M3 4l4 4-4 4M7 4l4 4-4 4" />
    <path v-else-if="name === 'arrow-left'" d="M13 8H3M3 8l4-4M3 8l4 4" />
    <path v-else-if="name === 'arrow-right'" d="M3 8h10M13 8l-4-4M13 8l-4 4" />
    <template v-else-if="name === 'search'">
      <circle cx="7" cy="7" r="4.5" />
      <path d="M13.5 13.5l-3-3" />
    </template>
    <template v-else-if="name === 'cog'">
      <!-- 6-tooth gear, stroke-only -->
      <path d="M6.76 1.62 L9.24 1.62 L9.53 3.56 A4.7 4.7 0 0 1 11.08 4.45 L12.91 3.74 L14.15 5.88 L12.61 7.10 A4.7 4.7 0 0 1 12.61 8.90 L14.15 10.12 L12.91 12.27 L11.08 11.55 A4.7 4.7 0 0 1 9.53 12.44 L9.24 14.38 L6.76 14.38 L6.47 12.44 A4.7 4.7 0 0 1 4.92 11.55 L3.09 12.27 L1.85 10.12 L3.39 8.90 A4.7 4.7 0 0 1 3.39 7.10 L1.85 5.88 L3.09 3.74 L4.92 4.45 A4.7 4.7 0 0 1 6.47 3.56 Z" />
      <circle cx="8" cy="8" r="2.2" />
    </template>
    <template v-else-if="name === 'log-out'">
      <path d="M6 2H3a1 1 0 0 0-1 1v10a1 1 0 0 0 1 1h3" />
      <path d="M10 5l4 3-4 3M5 8h9" />
    </template>
    <path v-else-if="name === 'plus'" d="M8 3v10M3 8h10" />
    <template v-else-if="name === 'more'">
      <circle cx="4" cy="8" r="0.75" fill="currentColor" stroke="none" />
      <circle cx="8" cy="8" r="0.75" fill="currentColor" stroke="none" />
      <circle cx="12" cy="8" r="0.75" fill="currentColor" stroke="none" />
    </template>
    <path v-else-if="name === 'x'" d="M4 4l8 8M12 4l-8 8" />
    <path v-else-if="name === 'menu'" d="M2 4h12M2 8h12M2 12h12" />
    <template v-else-if="name === 'copy'">
      <rect x="5" y="5" width="8" height="8" rx="1" />
      <path d="M3 11V4a1 1 0 0 1 1-1h7" />
    </template>
    <template v-else-if="name === 'sun'">
      <circle cx="8" cy="8" r="3" />
      <path d="M8 1.5v1.5M8 13v1.5M1.5 8h1.5M13 8h1.5M3.4 3.4l1.1 1.1M11.5 11.5l1.1 1.1M3.4 12.6l1.1-1.1M11.5 4.5l1.1-1.1" />
    </template>
    <path v-else-if="name === 'moon'" d="M13.5 9.5a5.5 5.5 0 0 1-7-7 6 6 0 1 0 7 7Z" />
    <path v-else-if="name === 'bell'" d="M8 2a5 5 0 0 1 5 5v2.5l1.5 2H1.5l1.5-2V7a5 5 0 0 1 5-5zM6 13.5a2 2 0 0 0 4 0" />
    <path v-else-if="name === 'package'" d="M8 2L14 5.5v7L8 16 2 12.5v-7L8 2zM8 2v14M2 5.5l6 3.5 6-3.5" />
    <path v-else-if="name === 'shield'" d="M8 2l5 2v4c0 3-2 5.5-5 6-3-.5-5-3-5-6V4l5-2z" />
    <template v-else-if="name === 'shield-check'">
      <path d="M8 2l5 2v4c0 3-2 5.5-5 6-3-.5-5-3-5-6V4l5-2z" />
      <path d="M6 8l1.5 1.5L10.5 6" />
    </template>
    <path v-else-if="name === 'external'" d="M10 2h4v4M14 2l-7 7M6 4H4a1 1 0 0 0-1 1v7a1 1 0 0 0 1 1h7a1 1 0 0 0 1-1v-2" />
    <path v-else-if="name === 'pause'" d="M5 3h2v10H5zM9 3h2v10H9z" />
    <path v-else-if="name === 'play'" d="M5 3l9 5-9 5V3z" />
    <template v-else-if="name === 'users'">
      <circle cx="5" cy="5.5" r="2.5" />
      <path d="M1 14a4 4 0 0 1 8 0" />
      <circle cx="10" cy="5.5" r="2.5" />
      <path d="M6 14a4 4 0 0 1 8 0" />
    </template>
    <path v-else-if="name === 'send'" d="M14 2L2 7l5 3 3 5 4-13zM7 9l4-4" />
    <path v-else-if="name === 'user'" d="M8 8a3 3 0 1 0 0-6 3 3 0 0 0 0 6zM2 14a6 6 0 0 1 12 0" />
    <path v-else-if="name === 'mail'" d="M2 4h12v10H2zM2 4l6 5 6-5" />
    <path v-else-if="name === 'tag'" d="M2 2h6l6 6-6 6-6-6V2zM5.5 5.5h.01" />
    <path v-else-if="name === 'alert-circle'" d="M8 2a6 6 0 1 0 0 12A6 6 0 0 0 8 2zM8 6v3M8 11v.5" />
    <path v-else-if="name === 'git-commit'" d="M6 8a2 2 0 1 0 4 0 2 2 0 0 0-4 0zM2 8h4M10 8h4" />
    <path v-else-if="name === 'corner-return'" d="M13 4v3a2 2 0 0 1-2 2H3M3 9l3-3M3 9l3 3" />
    <path v-else-if="name === 'pencil'" d="M11 2l3 3-9 9H2v-3l9-9z" />
    <path v-else-if="name === 'trash'" d="M3 5h10M5 5V3h6v2M6 8v5M10 8v5M4 5l1 9h6l1-9" />
    <template v-else-if="name === 'globe'">
      <circle cx="8" cy="8" r="6" />
      <path d="M2 8h12M8 2a9 6 0 0 0 0 12A9 6 0 0 0 8 2z" />
    </template>
    <path v-else-if="name === 'github'" fill="currentColor" stroke="none" d="M8 2C4.69 2 2 4.69 2 8c0 2.65 1.71 4.9 4.08 5.7.3.06.41-.13.41-.29v-1c-1.66.36-2-.8-2-.8-.28-.7-.67-.88-.67-.88-.55-.38.04-.37.04-.37.61.04.93.62.93.62.54.93 1.41.66 1.75.5.06-.39.21-.66.38-.81-1.33-.15-2.73-.67-2.73-2.96 0-.66.23-1.19.62-1.61-.06-.15-.27-.76.06-1.59 0 0 .5-.16 1.66.62.48-.14 1-.2 1.51-.2s1.03.06 1.51.2c1.16-.78 1.67-.62 1.67-.62.33.83.12 1.44.06 1.59.39.42.62.95.62 1.61 0 2.3-1.4 2.81-2.74 2.96.22.19.41.56.41 1.13v1.67c0 .16.11.35.42.3C12.29 12.9 14 10.65 14 8c0-3.31-2.69-6-6-6z" />
    <!-- Aliases / additional icons -->
    <template v-else-if="name === 'more-horizontal'">
      <circle cx="4" cy="8" r="0.75" fill="currentColor" stroke="none" />
      <circle cx="8" cy="8" r="0.75" fill="currentColor" stroke="none" />
      <circle cx="12" cy="8" r="0.75" fill="currentColor" stroke="none" />
    </template>
    <path v-else-if="name === 'trash-2'" d="M3 5h10M5 5V3h6v2M6 8v5M10 8v5M4 5l1 9h6l1-9" />
    <template v-else-if="name === 'check-circle'">
      <circle cx="8" cy="8" r="6" />
      <path d="M5.5 8l1.5 1.5 3.5-3.5" />
    </template>
    <template v-else-if="name === 'info'">
      <circle cx="8" cy="8" r="6" />
      <path d="M8 7.5v3.5" />
      <circle cx="8" cy="5.5" r="0.5" fill="currentColor" stroke="none" />
    </template>
    <template v-else-if="name === 'key-round'">
      <circle cx="5.5" cy="9.5" r="3.5" />
      <path d="M8 7.5l5.5-5.5" />
      <path d="M12 3.5l1.5 1.5" />
      <path d="M10.5 5l1 1" />
    </template>
    <template v-else-if="name === 'shield-off'">
      <path d="M3.4 4.4 3 5v3c0 3 2 5.5 5 6 1.4-.2 2.6-.9 3.5-1.9" />
      <path d="M8 2l5 2v.5" />
      <path d="M2.5 2.5l11 11" />
    </template>
    <template v-else-if="name === 'refresh-cw'">
      <path d="M13.5 2.5v4h-4" />
      <path d="M2.5 13.5v-4h4" />
      <path d="M13.5 6.5A6 6 0 0 0 4 3.5L2.5 5" />
      <path d="M2.5 9.5A6 6 0 0 0 12 12.5l1.5-1.5" />
    </template>
    <path v-else-if="name === 'zap'" d="M9.5 2L3 9h5l-1.5 5L14 7H9L9.5 2z" />
    <template v-else-if="name === 'clock'">
      <circle cx="8" cy="8" r="6" />
      <path d="M8 5v3l2 2" />
    </template>
    <template v-else-if="name === 'database'">
      <ellipse cx="8" cy="4.5" rx="5.5" ry="2" />
      <path d="M2.5 4.5v3c0 1.1 2.46 2 5.5 2s5.5-.9 5.5-2v-3" />
      <path d="M2.5 7.5v3c0 1.1 2.46 2 5.5 2s5.5-.9 5.5-2v-3" />
    </template>
    <polyline v-else-if="name === 'activity'" points="2,10 5,10 6,5 8,13 10,7 11,10 14,10" />
    <path v-else-if="name === 'download'" d="M8 2v8M5 7l3 3 3-3M2 12v1a1 1 0 0 0 1 1h10a1 1 0 0 0 1-1v-1" />
    <template v-else-if="name === 'credit-card'">
      <rect x="2" y="4" width="12" height="9" rx="1" />
      <path d="M2 7.5h12M5 11h2" />
    </template>
    <template v-else-if="name === 'file-text'">
      <path d="M4 2h6l4 4v8a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1V3a1 1 0 0 1 1-1z" />
      <path d="M10 2v4h4M5 9h6M5 12h4" />
    </template>
    <template v-else-if="name === 'alert-triangle'">
      <path d="M7.1 2.7L1.5 12.5a1 1 0 0 0 .9 1.5h11.2a1 1 0 0 0 .9-1.5L8.9 2.7a1 1 0 0 0-1.8 0z" />
      <path d="M8 6.5v3" />
      <circle cx="8" cy="11.5" r="0.5" fill="currentColor" stroke="none" />
    </template>
    <path v-else-if="name === 'chevrons-down'" d="M4 4l4 4 4-4M4 9l4 4 4-4" />
    <path v-else-if="name === 'chevrons-up'" d="M4 12l4-4 4 4M4 7l4-4 4 4" />
    <template v-else-if="name === 'circle-dot'">
      <circle cx="8" cy="8" r="6" />
      <circle cx="8" cy="8" r="1.5" fill="currentColor" stroke="none" />
    </template>
    <template v-else-if="name === 'git-commit-vertical'">
      <circle cx="8" cy="8" r="2" />
      <path d="M8 2v4M8 10v4" />
    </template>
    <template v-else-if="name === 'key'">
      <circle cx="5.5" cy="8" r="3" />
      <path d="M8.5 8h6M12.5 8v2M10.5 8v1.5" />
    </template>
    <path v-else-if="name === 'loader'" d="M8 2.5v2M8 11.5v2M2.5 8h2M11.5 8h2M4.3 4.3l1.4 1.4M10.3 10.3l1.4 1.4M4.3 11.7l1.4-1.4M10.3 5.7l1.4-1.4" />
    <template v-else-if="name === 'squares'">
      <rect x="2" y="2" width="5" height="5" rx="0.75" />
      <rect x="9" y="2" width="5" height="5" rx="0.75" />
      <rect x="2" y="9" width="5" height="5" rx="0.75" />
      <rect x="9" y="9" width="5" height="5" rx="0.75" />
    </template>
    <path v-else-if="name === 'slack'" d="M5 2v12M11 2v12M2 5h12M2 11h12" />
    <template v-else-if="name === 'discord'">
      <path d="M3 9a5 5 0 0 1 10 0" />
      <path d="M2 8h2v3H2z" />
      <path d="M12 8h2v3h-2z" />
    </template>
    <template v-else-if="name === 'book-open'">
      <path d="M2 4.5A1.5 1.5 0 0 1 3.5 3H8v10H3.5A1.5 1.5 0 0 1 2 11.5v-7z" />
      <path d="M14 4.5A1.5 1.5 0 0 0 12.5 3H8v10h4.5A1.5 1.5 0 0 0 14 11.5v-7z" />
      <path d="M8 3v10" />
    </template>
  </svg>
</template>
