<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, type HTMLAttributes } from "vue"
import { cn } from "@/shared/lib/utils"

/**
 * A data table. Below the `lg` breakpoint every row becomes a card (styles.css, "Tables as cards"): the first cell is the card's title, the other cells are
 * label/value lines and the last cell, when its column has no heading, is the row's actions. The labels come from the header cells, copied onto the body cells
 * as `data-label`, so a page writes its table once. `stack="false"` keeps a table a table on a phone (a table that is a grid of numbers, not a list of records).
 */
const props = withDefaults(defineProps<{
  class?: HTMLAttributes["class"]
  stack?: boolean
}>(), { stack: true })

const el = ref<HTMLTableElement | null>(null)
let queued = false
let watcher: MutationObserver | undefined

function label() {
  const table = el.value
  if (!table) return
  // A heading that is only for a screen reader (the actions column's) is not a label a person sees.
  const heads = Array.from(table.tHead?.rows[0]?.cells ?? [], (h) => {
    const visible = h.cloneNode(true) as HTMLElement
    visible.querySelectorAll(".sr-only").forEach((n) => n.remove())
    return visible.textContent?.trim() ?? ""
  })
  for (const body of Array.from(table.tBodies)) {
    for (const row of Array.from(body.rows)) {
      Array.from(row.cells).forEach((cell, i) => {
        const text = cell.colSpan === 1 ? heads[i] : ""
        if (text) cell.dataset.label = text
        else delete cell.dataset.label
        // A cell with nothing to show (an expander's, a column a row leaves blank) takes no line in a card.
        if (!cell.querySelector("*") && !cell.textContent?.trim()) cell.dataset.empty = ""
        else delete cell.dataset.empty
      })
      // The card's title is the row's first cell that has something in it.
      const cells = Array.from(row.cells)
      cells.forEach((cell) => delete cell.dataset.lead)
      const lead = cells.find((cell) => cell.dataset.empty === undefined)
      if (lead) lead.dataset.lead = ""
    }
  }
}
// A burst of changes (a list of rows replaced) is labelled once, after it.
const schedule = () => {
  if (queued) return
  queued = true
  queueMicrotask(() => {
    queued = false
    label()
  })
}

onMounted(() => {
  if (!props.stack || !el.value) return
  label()
  // Rows come and go with the data and the filters; only the tree's shape is watched, so writing an attribute does not call this again.
  watcher = new MutationObserver(schedule)
  watcher.observe(el.value, { childList: true, subtree: true, characterData: true })
})
onBeforeUnmount(() => {
  watcher?.disconnect()
})
</script>

<template>
  <div data-slot="table-container" class="relative w-full overflow-auto">
    <table ref="el" data-slot="table" :data-stack="stack ? '' : undefined" :class="cn('w-full caption-bottom text-sm', props.class)">
      <slot />
    </table>
  </div>
</template>
