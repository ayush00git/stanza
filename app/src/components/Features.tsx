type Feature = {
  index: string
  title: string
  body: string
}

const features: Feature[] = [
  {
    index: 'i',
    title: 'A line-aware editor',
    body: 'Write in lines, not paragraphs. Stanza keeps your breaks, indents, and spacing exactly as you set them, so the shape on the page is the shape you meant.',
  },
  {
    index: 'ii',
    title: 'Revision, line by line',
    body: 'Every change is tracked at the line level. Compare drafts the way an editor would — one turn of phrase at a time, with nothing lost between them.',
  },
  {
    index: 'iii',
    title: 'Publish, unadorned',
    body: 'Turn any piece into a clean, readable page. No chrome, no clutter, no theme to fight with. Just the words, set with care.',
  },
]

export default function Features() {
  return (
    <section
      id="features"
      className="border-t border-hairline bg-paper-deep/50"
    >
      <div className="mx-auto max-w-5xl px-6 py-20 sm:py-24">
        <h2 className="max-w-md font-display text-3xl font-normal leading-tight tracking-[-0.01em] text-ink sm:text-4xl">
          The studio, and what it keeps out of your way.
        </h2>

        <div className="mt-14 grid gap-px overflow-hidden rounded-lg border border-hairline bg-hairline sm:grid-cols-3">
          {features.map((feature) => (
            <article key={feature.index} className="bg-paper p-8">
              <span className="font-mono text-sm text-accent">
                {feature.index}.
              </span>
              <h3 className="mt-6 font-display text-xl font-medium text-ink">
                {feature.title}
              </h3>
              <p className="mt-3 text-[0.95rem] leading-relaxed text-muted">
                {feature.body}
              </p>
            </article>
          ))}
        </div>
      </div>
    </section>
  )
}
