document.addEventListener('alpine:init', () => {
  Alpine.data('tripFinder', () => ({
    terrain: 'all', trips: [], loading: false, loaded: false, error: '',
    get summary() { return `${this.trips.length} ${this.trips.length === 1 ? 'trip' : 'trips'} to explore`; },
    async load() {
      if (this.loading) return;
      this.loading = true;
      this.error = '';
      const controller = new AbortController();
      const timeout = setTimeout(() => controller.abort(), 10000);
      try {
        const response = await fetch(`/examples/alpine/trips?${new URLSearchParams({terrain: this.terrain})}`, {
          headers: {Accept: 'application/json'}, signal: controller.signal,
        });
        if (!response.ok) throw new Error('Request failed');
        const data = await response.json();
        if (!Array.isArray(data.trips)) throw new Error('Invalid response');
        this.trips = data.trips;
        this.loaded = true;
      } catch (_) {
        this.error = 'We could not load the trips. Check your connection and try again.';
      } finally {
        clearTimeout(timeout);
        this.loading = false;
      }
    },
  }));
});
