<script setup lang="ts">
import { computed } from 'vue'
import { useRouter } from 'vue-router'
import { useQuery } from '@tanstack/vue-query'
import IssueSection from './IssueSection.vue'
import { issueSectionQueries } from './sectionQueries'

// How the issue's events spread over each tag value. A value links to the
// issue list filtered by that tag.
const props = defineProps<{ issueId: string }>()

const router = useRouter()

const { data: issueTags } = useQuery({
  queryKey: computed(() => issueSectionQueries.tags(props.issueId).queryKey),
  queryFn: (ctx) => issueSectionQueries.tags(props.issueId).queryFn(ctx),
  enabled: computed(() => !!props.issueId),
})
</script>

<template>
  <IssueSection v-if="(issueTags?.length ?? 0) > 0" section-key="tags" title="Tags">
    <template #head>
      <span class="section__count">{{ issueTags!.length }} keys</span>
    </template>
    <div class="tags-dist">
      <div v-for="tag in issueTags" :key="tag.key" class="tag-dist">
        <div class="tag-dist__head">
          <span class="tag-dist__key">{{ tag.key }}</span>
          <span class="tag-dist__total">{{ tag.total.toLocaleString() }} events</span>
        </div>
        <div
          v-for="tv in tag.values"
          :key="tv.value"
          class="tag-val"
        >
          <a
            class="tag-val__label"
            :href="`/issues?tag_key=${encodeURIComponent(tag.key)}&tag_value=${encodeURIComponent(tv.value)}`"
            @click.prevent="router.push({ path: '/issues', query: { tag_key: tag.key, tag_value: tv.value } })"
          >{{ tv.value }}</a>
          <div class="tag-val__bar">
            <div class="tag-val__fill" :style="{ transform: `scaleX(${tv.pct / 100})` }" />
          </div>
          <span class="tag-val__pct">{{ tv.pct }}%</span>
        </div>
      </div>
    </div>
  </IssueSection>
</template>
