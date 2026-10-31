<script>
  import DatabaseList from '../../lib/DatabaseList.svelte';

  /** A project's Databases tab: the shared list, narrowed to this project. */
  let { project, onchange } = $props();
</script>

<DatabaseList {project} {onchange} />
