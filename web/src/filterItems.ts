export type ItemFilter = "all" | "pull_request" | "issue";

type FilterableItem = {
  kind: Exclude<ItemFilter, "all">;
};

export function filterItems<T extends FilterableItem>(
  items: readonly T[],
  filter: ItemFilter,
): readonly T[] {
  if (filter === "all") {
    return items;
  }

  return items.filter((item) => item.kind === filter);
}
