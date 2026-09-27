/** A response belongs to the page and request that sent it. Page changes are
 * recorded during render, before an effect can clear stale visible state. */
export class WebsitePromptRunGuard {
  private page: string;
  private sequence = 0;

  constructor(page: string) { this.page = page; }

  pageChanged(page: string) {
    if (page !== this.page) { this.page = page; this.sequence += 1; }
  }

  begin(page: string) {
    this.pageChanged(page);
    return { page, sequence: ++this.sequence };
  }

  isCurrent(run: { page: string; sequence: number }) {
    return run.page === this.page && run.sequence === this.sequence;
  }

  invalidate() { this.sequence += 1; }
}
