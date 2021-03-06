package pop

import (
	"github.com/genetic-algorithms/mendel-go/config"
	"log"
	"sort"
	"math/rand"
	"fmt"
	"math"
	"github.com/genetic-algorithms/mendel-go/utils"
	"github.com/genetic-algorithms/mendel-go/dna"
	"sync"
	"encoding/json"
	"runtime/debug"
	"github.com/genetic-algorithms/mendel-go/random"
	"strings"
	"strconv"
)

type RecombinationType uint8
const (
	//CLONAL RecombinationType = 1   <-- have not needed these yet, uncomment when we do
	//SUPPRESSED RecombinationType = 2
	FULL_SEXUAL RecombinationType = 3
)


// Used as the elements for the Sort routine used for selection, and as indirection to point to individuals in PopulationPart objects
type IndivRef struct {
	Indiv *Individual
}
type ByFitness []IndivRef
func (a ByFitness) Len() int           { return len(a) }
func (a ByFitness) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a ByFitness) Less(i, j int) bool { return a[i].Indiv.PhenoFitness < a[j].Indiv.PhenoFitness }


// Population tracks the tribes and global info about the population. It also handles population-wide actions like mating and selection.
type Population struct {
	TribeNum uint32	// the tribe number
	Parts []*PopulationPart		// Subsets of the pop that are mated in parallel. This contains the backing array for IndexRefs.
	IndivRefs []IndivRef	// References to individuals in the indivs array. This level of indirection allows us to sort this list, truncate it after selection, and refer to indivs in PopulationParts, all w/o copying Individual objects.

	TargetSize uint32        // the target size of this population after selection
	Done bool				 // true if went extinct or hit its pop max
	BottleNecks *Bottlenecks // the bottlenecks this pop should go thru
	Num_offspring float64    // Average number of offspring each individual should have (so need to multiple by 2 to get it for the mating pair). Calculated from config values Fraction_random_death and Reproductive_rate.
	LBsPerChromosome uint32  // How many linkage blocks in each chromosome. For now the total number of LBs must be an exact multiple of the number of chromosomes

	// Stats
	ActualAvgOffspring float64       // The average number of offspring each individual from last generation actually had in this generation
	PreSelGenoFitnessMean float64                                       // The average fitness of all of the individuals (before selection) due to their genomic mutations
	PreSelGenoFitnessVariance float64                                   //
	PreSelGenoFitnessStDev    float64                                   // The standard deviation from the GenoFitnessMean
	EnvironNoise              float64                                   // randomness applied to geno fitness calculated from PreSelGenoFitnessVariance, heritability, and non_scaling_noise

	MeanFitness, MinFitness, MaxFitness float64                         // cache summary info about the individuals
	TotalNumMutations uint64
	MeanNumMutations float64

	MeanNumDeleterious, MeanNumNeutral, MeanNumFavorable  float64       // cache some of the stats we usually gather

	MeanNumDelAllele, MeanNumFavAllele float64       // cache some of the stats we usually gather
}


// PopulationFactory creates a new population. If genNum==0 it creates the special genesis population.
func PopulationFactory(prevPop *Population, genNum, tribeNum, partsPerPop uint32) *Population {
	var targetSize uint32
	if prevPop != nil {
		if prevPop.Done { return prevPop }
		targetSize = Mdl.PopulationGrowth(prevPop, genNum)
	} else {
		// This is the 1st generation, so set the size from the config param
		targetSize = config.Cfg.Basic.Pop_size
	}
	p := &Population{
		TribeNum: tribeNum,
		Parts: make([]*PopulationPart, 0, partsPerPop), 	// allocate the array for the ptrs to the parts. The actual part objects will be appended below
		TargetSize: targetSize,
	}
	if PopulationGrowthModelType(strings.ToLower(config.Cfg.Population.Pop_growth_model)) == MULTI_BOTTLENECK_POPULATON_GROWTH {
		if prevPop != nil {
			p.BottleNecks = prevPop.BottleNecks // pass the bottleneck list down from the prev pop
		} else {
			p.BottleNecks = ParseMultipleBottlenecks(config.Cfg.Population.Multiple_Bottlenecks)
		}
	}

	fertility_factor := 1. - config.Cfg.Selection.Fraction_random_death
	p.Num_offspring = config.Cfg.Population.Reproductive_rate * fertility_factor 	// the default for Num_offspring is 2

	p.LBsPerChromosome = uint32(config.Cfg.Population.Num_linkage_subunits / config.Cfg.Population.Haploid_chromosome_number)	// main.initialize() already confirmed it was a clean multiple

	if genNum == 0 {
		// Create individuals (with no mutations) for the genesis generation. (For subsequent generations, individuals are added to the Population object via Mate().
		p.Parts = append(p.Parts, PopulationPartFactory(targetSize, p))    // for gen 0 we only need 1 part because that doesn't have offspring added to it during Mate()
		p.makeAndFillIndivRefs()
	} else {
		for i:=1; i<= cap(p.Parts); i++ { p.Parts = append(p.Parts, PopulationPartFactory(0, p)) }
		// Mate() will populate PopulationPart with Individuals and run makeAndFillIndivRefs()
	}

	return p
}


// Not currently used, but kept here in case we want to reuse populations - Reinitialize recycles a population object for another generation. This saves freeing and reallocating a lot of objects
func (p *Population) Reinitialize(prevPop *Population, genNum uint32) *Population {
	if p.Done { return p }
	// Reinitialize is never called on the genesis population
	p.TargetSize = Mdl.PopulationGrowth(prevPop, genNum)

	// Truncate the IndivRefs slice. makeAndFillIndivRefs() will make it again if not big enough.
	p.IndivRefs = p.IndivRefs[:0]
	//Note: the above won't allow the backing array to be GC'd, but that's what we want, because likely we will be able to reuse it.
	//		To really free it: p.IndivRefs = nil

	// It already has PopulationPart objects, reinitialize those too
	for _, part := range p.Parts {
		part.Reinitialize()
	}

	// These member vars stay the same: Num_offspring, LBsPerChromosome

	// Zero out stats
	p.ActualAvgOffspring = 0.0
	p.PreSelGenoFitnessMean = 0.0
	p.PreSelGenoFitnessVariance = 0.0
	p.PreSelGenoFitnessStDev = 0.0
	p.EnvironNoise = 0.0
	p.MeanFitness = 0.0
	p.MinFitness = 0.0
	p.MaxFitness = 0.0
	p.TotalNumMutations = 0
	p.MeanNumMutations = 0.0
	p.MeanNumDeleterious = 0.0
	p.MeanNumNeutral = 0.0
	p.MeanNumFavorable  = 0.0
	// We do not need to reinitialize these each generation, because they never change after gen 0
	//p.MeanNumDelAllele = 0.0
	//p.MeanNumFavAllele = 0.0

	return p
}


// Size returns the current number of individuals in this population
func (p *Population) GetCurrentSize() uint32 {
	return uint32(len(p.IndivRefs))
}


// These are the different algorithms for generating initial alleles
type GenerateInitialAllelesType func(p *Population, uniformRandom *rand.Rand)

// GenerateAllUniqueInitialAlleles creates unique initial contrasting allele pairs (if specified by the config file) on indivs in the population
func GenerateAllUniqueInitialAlleles(p *Population, uniformRandom *rand.Rand) {
	if config.Cfg.Population.Num_contrasting_alleles == 0 || config.Cfg.Population.Initial_alleles_pop_frac <= 0.0 { return }	// nothing to do

	// Loop thru individuals, and skipping or choosing individuals to maintain a ratio close to Initial_alleles_pop_frac
	// Note: config.Validate() already confirms Initial_alleles_pop_frac is > 0 and <= 1.0
	var numWithAlleles uint32 = 0 		// so we can calc the ratio so far of indivs we've given alleles to vs. number of indivs we've processed
	var numLBsWithAlleles uint32
	var numProcessedLBs uint32
	for i, indRef := range p.IndivRefs {
		ind := indRef.Indiv
		var ratioSoFar float64
		if i > 0 { ratioSoFar = float64(numWithAlleles) / float64(i) }
		// else ratioSoFar = 0
		if ratioSoFar <= config.Cfg.Population.Initial_alleles_pop_frac {
			// Give this indiv alleles to boost the ratio closer to Initial_alleles_pop_frac
			config.Verbose(9, "Giving initial contrasting allele to individual %v", i)
			numLBsWithAlleles, numProcessedLBs = ind.AddInitialContrastingAlleles(config.Cfg.Population.Num_contrasting_alleles, uniformRandom)
			numWithAlleles++
		}
		// else we don't give this indiv alleles to bring the ratio down closer to Initial_alleles_pop_frac
	}

	config.Verbose(2, "Initial alleles given to faction %v of individuals (%v/%v). Each individual got alleles on fraction %v of LBs (%v/%v)", float64(numWithAlleles)/float64(p.GetCurrentSize()), numWithAlleles, p.GetCurrentSize(),float64(numLBsWithAlleles)/float64(numProcessedLBs), numLBsWithAlleles, numProcessedLBs)
}

type FractionFrequency struct {
	alleleFraction float64
	frequency float64
}

func ParseInitialAllelesFrequencies(frequencies string) (freqList []FractionFrequency) {
	// The input parameter should look like: 0.25:0.1, 0.5:0.25, 0.25:0.5
	errorStr := fmt.Sprintf("Error: if initial_allele_fitness_model==%s, then initial_alleles_frequencies must be like: alfrac1:freq1, alfrac2:freq2, ...", string(VARIABLE_FREQ_INITIAL_ALLELES))
	if strings.TrimSpace(frequencies) == "" { log.Fatal(errorStr) }
	freqList = make([]FractionFrequency, 0)
	pairs := strings.Split(frequencies, ",")
	var alleleTotal float64
	for _, p := range pairs {
		parts := strings.Split(p, ":")
		if len(parts) < 2 { log.Fatal(errorStr) }
		var alleleFraction, frequency float64
		var err error
		alleleFraction, err = strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
		if err != nil  { log.Fatalf("Error: if initial_allele_fitness_model==%s, then initial_alleles_frequencies must be like: alfrac1:freq1, alfrac2:freq2, .... Parsing error: %v", string(VARIABLE_FREQ_INITIAL_ALLELES), err) }
		if alleleFraction <= 0.0 || alleleFraction > 1.0 { log.Fatalf("Error: initial_alleles_frequencies must contain allele fractions that are > 0.0 and <= 1.0. Bad allele fraction: %v", alleleFraction) }
		frequency, err = strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
		if err != nil  { log.Fatalf("Error: if initial_allele_fitness_model==%s, then initial_alleles_frequencies must be like: alfrac1:freq1, alfrac2:freq2, .... Parsing error: %v", string(VARIABLE_FREQ_INITIAL_ALLELES), err) }
		if frequency <= 0.0 || frequency > 0.5 { log.Fatalf("Error: initial_alleles_frequencies must contain frequences that are > 0.0 and <= 0.5. Bad frequency: %v", frequency) }
		freqList = append(freqList, FractionFrequency{alleleFraction:alleleFraction, frequency:frequency})
		alleleTotal += alleleFraction
	}
	if alleleTotal != 1.0 { log.Fatalf("Error: the allele fractions specified in initial_alleles_frequencies must add up to 1.0, not %v", alleleTotal) }
	return
}

// GenerateVariableFreqInitialAlleles creates initial contrasting allele pairs according to the frequencies specified in Initial_alleles_frequencies
func GenerateVariableFreqInitialAlleles(p *Population, uniformRandom *rand.Rand) {
	if config.Cfg.Population.Num_contrasting_alleles == 0 { return }	// nothing to do
	// Parse the input parameter
	freqList := ParseInitialAllelesFrequencies(config.Cfg.Population.Initial_alleles_frequencies)
	config.Verbose(1, "Generating initial contrasting alleles for: %v", freqList)

	// Loop thru each fraction/fequency pair
	for _, fracFreq := range freqList {
		numAlleles := utils.RoundInt(float64(config.Cfg.Population.Num_contrasting_alleles) * fracFreq.alleleFraction)
		numIndivs := utils.RoundInt(float64(p.GetCurrentSize()) * fracFreq.frequency * 2.0)		// times 2.0 because frequency is the fraction of chromosone sets (twice the pop) and each initial allele only goes on 1 LB in each indiv
		if numIndivs > int(p.GetCurrentSize()) {
			if numIndivs > int(p.GetCurrentSize() + 1) { log.Printf("Internal Error: numIndivs of %d is > than population size", numIndivs) }
			numIndivs = int(p.GetCurrentSize())
		}
		allIndivs := numIndivs == int(p.GetCurrentSize())

		// Create numAlleles and put each of them on numIndivs
		for i:=1; i<=numAlleles; i++ {
			favMutn, delMutn := dna.CreateInitialAllelePair(utils.GlobalUniqueInt, uniformRandom)

			// Randomly choose a chromosome and LB position for this allele pair to go on
			lbIndex := uniformRandom.Intn(int(config.Cfg.Population.Num_linkage_subunits - 1))   // 0 to numLBs-1
			chromoIndex := lbIndex / int(p.LBsPerChromosome) 	// 0 to numChr-1
			lbIndexOnChr := lbIndex % int(p.LBsPerChromosome) 	// 0 to LBsPerChromosome-1

			// To avoid always adding alleles to the same indivs, create a shuffled slice of indices into the population
			var indivIndices []int
			if !allIndivs { indivIndices = uniformRandom.Perm(int(p.GetCurrentSize())) }  // as optimization don't mix pop order if we are going to give the allele to everyone anyway

			// Loop thru the correct number of indivs, adding the allele pair to each indiv
			for j := 0; j < numIndivs; j++ {
				var ind *Individual
				if allIndivs {
					ind = p.IndivRefs[j].Indiv
				} else {
					ind = p.IndivRefs[indivIndices[j]].Indiv
				}
				ind.AddInitialAllelePair(chromoIndex, lbIndexOnChr, favMutn, delMutn)
			}
		}
	}
}

// Mate mates all the pairs of the population, choosing the linkage block at each linkage block position randomly from
// the mom or dad according to the crossover model (as in meiosis), and fills in the new/resulting population.
// The mating process is:
// - randomly choose 2 parents
// - determine number of offspring
// - for each offspring:
//   - for each LB section, choose 1 LB from dad (from either his dad or mom) and 1 LB from mom (from either her dad or mom)
//   - add new mutations to random LBs
//   - add offspring to new population
func (p *Population) Mate(newP *Population, uniformRandom *rand.Rand) {
	if p.Done { return }
	config.Verbose(4, "Mating the population of %d individuals...\n", p.GetCurrentSize())

	// To prepare for mating, create a shuffled slice of indices into the parent population
	parentIndices := uniformRandom.Perm(int(p.GetCurrentSize()))

	// Divide parentIndices into segments (whose size is an even number) and schedule a go routine to mate each segment
	// Note: runtime.GOMAXPROCS(runtime.NumCPU()) is the default, but this statement can be modified to set a different number of CPUs to use
	segmentSize := utils.RoundToEven( float64(len(parentIndices)) / float64(len(newP.Parts)) )
	if segmentSize <= 0 { segmentSize = 2 }
	segmentStart := 0
	highestIndex := len(parentIndices) - 1
	config.Verbose(4, "Scheduling %v population parts concurrently with segmentSize=%v, highestIndex=%v", len(newP.Parts), segmentSize, highestIndex)
	var waitGroup sync.WaitGroup
	for i := range newP.Parts {
		newPart := newP.Parts[i] 		// part can't be declared in the for stmt because it would change before some of the go routines start. See https://github.com/golang/go/wiki/CommonMistakes
		if segmentStart <= highestIndex {
			// We still have more elements in parentIndices to mate
			var newRandom *rand.Rand
			if i == 0 {
				// Let the 1st thread use the main uniformRandom generator. This has the effect that if there is only 1 thread, we will have the same
				// sequence of random numbers that we had before concurrency was added (so we can verify the results).
				newRandom = uniformRandom
			} else {
				newRandom = random.RandFactory()
			}

			beginIndex := segmentStart		// just to be careful, copying segmentStart to a local var so it is different for each go routine invocation
			var endIndex int
			if i < len(newP.Parts) - 1 {
				endIndex = utils.MinInt(segmentStart + segmentSize - 1, highestIndex)
			} else {
				// the last partition, so do everything that is left
				endIndex = highestIndex
			}

			// Choose a range of the mutation id's for this part - have to make sure it won't exceed this
			numMuts := uint64(float64(endIndex - beginIndex + 1) * p.Num_offspring * config.Cfg.Mutations.Mutn_rate * 1.5)
			if numMuts <= 100 { numMuts = numMuts * 2}		// with small number the randomness of Poisson distribution can vary more
			//log.Printf("DEBUG: donating %d mutation ids for %d individuals", numMuts, endIndex - beginIndex + 1)

			// Start the concurrent routine for this part of the pop
			waitGroup.Add(1)
			go newPart.Mate(p, parentIndices[beginIndex:endIndex +1], utils.GlobalUniqueInt.DonateRange(numMuts), newRandom, &waitGroup)

			// Prep for next iteration
			segmentStart = endIndex + 1
		}
		// else we are out of elements in parentIndices so do not do anything
	}

	// Wait for all of the Mate functions to complete
	waitGroup.Wait()

	newP.makeAndFillIndivRefs()	// now that we are done creating new individuals, fill in the array of references to them

	// Save off the average num offspring for stats, before we select out individuals
	newP.ActualAvgOffspring = float64(newP.GetCurrentSize()) / float64(p.GetCurrentSize())

	newP.PreSelGenoFitnessMean, newP.PreSelGenoFitnessVariance, newP.PreSelGenoFitnessStDev = newP.PreSelectFitnessStats()
}


// Select removes the least fit individuals in the population
func (p *Population) Select(uniformRandom *rand.Rand) {
	if p.Done { return }
	config.Verbose(4, "Select: eliminating %d individuals to try to maintain a population of %d...\n", p.GetCurrentSize()-p.TargetSize, p.TargetSize)

	// Calculate noise factor to get pheno fitness of each individual
	herit := config.Cfg.Selection.Heritability
	p.EnvironNoise = math.Sqrt(p.PreSelGenoFitnessVariance * (1.0-herit) / herit + math.Pow(config.Cfg.Selection.Non_scaling_noise,2))
	Mdl.ApplySelectionNoise(p, p.EnvironNoise, uniformRandom) 		// this sets PhenoFitness in each of the individuals

	// Sort the indexes of the Indivs array by fitness, and mark the least fit individuals as dead
	p.sortIndexByPhenoFitness()		// this sorts p.IndivRefs
	numAlreadyDead := p.getNumDead()

	if numAlreadyDead > 0 {
		config.Verbose(3, "%d individuals died (fitness < 0, or < 1 when using spps) as a result of mutations added during mating", numAlreadyDead)
	}

	currentSize := uint32(len(p.IndivRefs))
	//log.Printf("DEBUG: eliminating %d individuals in selection", int32(currentSize) - int32(p.TargetSize))

	if currentSize > p.TargetSize {
		numEliminate := currentSize - p.TargetSize

		if numAlreadyDead < numEliminate {
			// Mark those that should be eliminated dead. They are sorted by fitness in ascending order, so mark the 1st ones dead.
			for i := uint32(0); i < numEliminate; i++ {
				p.IndivRefs[i].Indiv.Dead = true
			}
		}
	}
	numDead := p.getNumDead()		// under certain circumstances this could be > the number we wanted to select out
	p.ReportDeadStats()
	p.IndivRefs = p.IndivRefs[numDead:]		// re-slice IndivRefs to eliminate the dead individuals

	// We can leave the indivs array sparse (with dead individuals in it), because the IndivRefs array only points to live entries in indivs,
	// and the indivs array will soon be GC'd or reused.

	return
}


// Returns true if this pop has gone extinct or reached its pop max
func (p *Population) IsDone(doLog bool) bool {
	popMaxIsSet := PopulationGrowthModelType(strings.ToLower(config.Cfg.Population.Pop_growth_model))==EXPONENTIAL_POPULATON_GROWTH && config.Cfg.Population.Max_pop_size>0
	popMax := config.Cfg.Population.Max_pop_size
	if popMaxIsSet && p.GetCurrentSize() >= popMax {
		if doLog { log.Printf("Tribe %d has reached the max specified value of %d. Stopping this tribe.", p.TribeNum, popMax) }
		return true
	} else if (RecombinationType(config.Cfg.Population.Recombination_model) == FULL_SEXUAL && p.GetCurrentSize() < 2) || p.GetCurrentSize() == 0 {
		// Above checks if we don't have enough individuals to mate
		if doLog { log.Printf("Tribe %d is extinct. Stopping this tribe.", p.TribeNum) }
		return true
	} else if aveFit, _, _, _, _ := p.GetFitnessStats(); aveFit < config.Cfg.Computation.Extinction_threshold {
		// Above checks if the the tribe's fitness is below the threshold
		if doLog { log.Printf("Tribe %d fitness is below the extinction threshold of %.3f. Stopping this tribe.", p.TribeNum, config.Cfg.Computation.Extinction_threshold) }
		return true
	}
	return false
}


// getNumDead returns the current number of dead individuals in this population
func (p *Population) getNumDead() uint32 {
	// We assume at this point p.IndivRefs is sorted by ascending fitness, so only need to count until we hit a non-dead individual
	for i, indRef := range p.IndivRefs {
		if !indRef.Indiv.Dead { return uint32(i) }
	}
	return uint32(len(p.IndivRefs))
}


// FreeParentRefs eliminates the reference to these 2 parents so gc can reclaim them because we don't need them any more.
// This is called by populationpart.Mate() after mating these 2 parents.
func (p *Population) FreeParentRefs(dadIndex int, momIndex int) {
	// Note: the 2 lines to nil out the indiv reference reduce memory usage, because I think the corresponding PopulationPart ptrs are freed in FreeChildrenPtrs
	//		a ptr to the individual, and we have no way of finding that index (w/o storing more info).
	//p.parentPop.IndivRefs[dadI].Indiv.Free()	// <- this doesn't help any more than setting the Indiv ptr to nil
	p.IndivRefs[dadIndex].Indiv = nil
	//p.parentPop.IndivRefs[momI].Indiv.Free()
	p.IndivRefs[momIndex].Indiv = nil
}


// ApplySelectionNoiseType functions add environmental noise and selection noise to the GenoFitness to set the PhenoFitness of all of the individuals of the population
type ApplySelectionNoiseType func(p *Population, envNoise float64, uniformRandom *rand.Rand)

// ApplyTruncationNoise only adds environmental noise (no selection noise)
func ApplyFullTruncationNoise(p *Population, envNoise float64, uniformRandom *rand.Rand) {
	/*
	Full truncation only adds a small amount of randomness, envNoise, which is calculated the 2 input parameters heritability and non_scaling_noise.
	Then the individuals are ordered by the resulting PhenoFitness, and the least fit are eliminated to achieve the desired population size.
	This makes full truncation the most efficient selection model, and unrealistic unless envNoise is set rather high.
	*/
	//for _, ind := range p.indivs {
	for _, indRef := range p.IndivRefs {
		ind := indRef.Indiv
		if ind.Dead {
			ind.PhenoFitness = 0.0
		} else {
			ind.PhenoFitness = ind.GenoFitness + uniformRandom.Float64() * envNoise
		}
	}
}

// ApplyUnrestrictProbNoise adds environmental noise and unrestricted probability selection noise
func ApplyUnrestrictProbNoise(p *Population, envNoise float64, uniformRandom *rand.Rand) {
	/*
	For unrestricted probability selection (UPS), divide the phenotypic fitness by a uniformly distributed random number prior to
	ranking and truncation.  This procedure allows the probability of surviving and reproducing in the next generation to be
	related to phenotypic fitness and also for the correct number of individuals to be eliminated to maintain a constant
	population size.
	*/

	/* Apply the environmental noise in a separate loop if you want the random num sequence to match that of spps (in which case the 2
		models give exactly the same results if reproductive_rate is not very small.
	for _, ind := range p.Indivs {
		ind.PhenoFitness = ind.GenoFitness + (uniformRandom.Float64() * envNoise)
	}
	*/

	for _, indRef := range p.IndivRefs {
		ind := indRef.Indiv
		if ind.Dead {
			ind.PhenoFitness = 0.0
		} else {
			//rnd1 := uniformRandom.Float64()
			ind.PhenoFitness = ind.GenoFitness + (uniformRandom.Float64() * envNoise)
			//rnd2 := uniformRandom.Float64()
			ind.PhenoFitness = ind.PhenoFitness / (uniformRandom.Float64() + 1.0e-15)
		}
		//ind.PhenoFitness = fit * uniformRandom.Float64()  // this has also been suggested instead of the line above, the results are pretty similar
	}
}

// ApplyProportProbNoise adds environmental noise and strict proportionality probability selection noise
func ApplyProportProbNoise(p *Population, envNoise float64, uniformRandom *rand.Rand) {
	/*
	For strict proportionality probability selection (SPPS), rescale (normalize) the phenotypic fitness values such that the maximum value is one.
	Then divide the scaled phenotypic fitness by a uniformly distributed random number prior to ranking and truncation.
	Allow only those individuals to reproduce whose resulting ratio of scaled phenotypic fitness to the random number value
	exceeds one.  This approach ensures that no individual automatically survives to reproduce regardless of their GenoFitness.
	But it restricts the percentage of the offspring that can survive to approximately 70-80% (it depends on the spread of the fitness).
	Therefore, when the reproductive_rate is low (approx < 1.4), the number of surviving offspring may not be large enough to sustain a constant population size.
	 */

	// First find max individual fitness (after applying the environmental noise)
	var maxFitness = 0.0
	for _, indRef := range p.IndivRefs {
		ind := indRef.Indiv
		if ind.Dead {
			ind.PhenoFitness = 0.0
		} else {
			ind.PhenoFitness = ind.GenoFitness + (uniformRandom.Float64() * envNoise)
		}
		maxFitness = utils.MaxFloat64(maxFitness, ind.PhenoFitness)
	}
	// Verify maxFitness is not zero so we can divide by it below
	if maxFitness <= 0.0 {
		log.Fatalf("Max individual fitness is <= 0 (%v), so whole population must be dead. Exiting.", maxFitness)
		return		// if maxFitness==0, the all ind's PhenoFitness<=0 so we don't have to normalize them
	}

	// Normalize the pheno fitness
	for _, indRef := range p.IndivRefs {
		ind := indRef.Indiv
		// The 1st division below produces values in the range (minFitness/maxFitness) - 1. The 2nd division gets the ratio of the
		// result and a random num 0 - 1. Since minFitness-maxFitness tends to be small (typically about 0.1), on average most results
		// will be > than the random num, therefore the ratio of most will be > 1.0, so few will get marked dead.
		ind.PhenoFitness = ind.PhenoFitness / maxFitness / (uniformRandom.Float64() + 1.0e-15)
		if ind.PhenoFitness < 1.0 { ind.Dead = true }
	}
}

// ApplyPartialTruncationNoise adds environmental noise and partial truncation selection noise
func ApplyPartialTruncationNoise(p *Population, envNoise float64, uniformRandom *rand.Rand) {
	/*
	For partial truncation selection, divide the phenotypic fitness by theta + ((1. - theta) * uniformRandom)
	prior to ranking and truncation, where theta is the parameter
	partial_truncation_value.  This selection scheme is intermediate between full truncation selection and unrestricted
	probability selection.  The procedure allows for the correct number of individuals to be eliminated to maintain a constant
	population size.
	*/
	for _, indRef := range p.IndivRefs {
		ind := indRef.Indiv
		if ind.Dead {
			ind.PhenoFitness = 0.0
		} else {
			ind.PhenoFitness = ind.GenoFitness + (uniformRandom.Float64() * envNoise)
			ind.PhenoFitness = ind.PhenoFitness / (config.Cfg.Selection.Partial_truncation_value + ((1. - config.Cfg.Selection.Partial_truncation_value) * uniformRandom.Float64()))
		}
	}
}


// PopulationGrowthType takes in the current population and generation number and returns the target pop size for the next gen
type PopulationGrowthType func(prevPop *Population, genNum uint32) uint32

// NoPopulationGrowth returns the same pop size as the previous generation
func NoPopulationGrowth(prevPop *Population, _ uint32) uint32 {
	return prevPop.TargetSize
}

// ExponentialPopulationGrowth returns the previous pop size times the growth rate
func ExponentialPopulationGrowth(prevPop *Population, _ uint32) uint32 {
	return uint32(math.Ceil(config.Cfg.Population.Pop_growth_rate * float64(prevPop.TargetSize)))
}

// CapacityPopulationGrowth uses an equation in which the pop size approaches the carrying capacity
func CapacityPopulationGrowth(prevPop *Population, _ uint32) uint32 {
	// mendel-f90 calculates the new pop target size as ceiling(pop_size * (1. + pop_growth_rate * (1. - pop_size/carrying_capacity) ) )
	newTargetSize := uint32(math.Ceil( float64(prevPop.TargetSize) * (1.0 + config.Cfg.Population.Pop_growth_rate * (1.0 - float64(prevPop.TargetSize)/float64(config.Cfg.Population.Carrying_capacity)) ) ))
	return newTargetSize
}

// FoundersPopulationGrowth increases the pop size exponentially until it reaches the carrying capacity, and supports bottlenecks
func FoundersPopulationGrowth(prevPop *Population, genNum uint32) uint32 {
	var newTargetSize uint32
	if config.Cfg.Population.Bottleneck_generation == 0 || genNum < config.Cfg.Population.Bottleneck_generation {
		// We are before the bottleneck so use 1st growth rate
		newTargetSize = uint32(math.Ceil(config.Cfg.Population.Pop_growth_rate * float64(prevPop.TargetSize)))
	} else if genNum >= config.Cfg.Population.Bottleneck_generation && genNum < config.Cfg.Population.Bottleneck_generation + config.Cfg.Population.Num_bottleneck_generations {
		// We are in the bottleneck range
		newTargetSize = config.Cfg.Population.Bottleneck_pop_size
	} else {
		// We are after the bottleneck so use 2nd growth rate
		newTargetSize = uint32(math.Ceil(config.Cfg.Population.Pop_growth_rate2 * float64(prevPop.TargetSize)))
	}
	newTargetSize = utils.MinUint32(newTargetSize, config.Cfg.Population.Carrying_capacity) 	// do not want it exceeding the carrying capacity
	return newTargetSize
}

// MultiBottleneckPopulationGrowth is like founders, except supports an arbitrary number of bottlenecks
func MultiBottleneckPopulationGrowth(prevPop *Population, genNum uint32) uint32 {
	var newTargetSize uint32
	pb := prevPop.BottleNecks
	curPB := pb.CurrentBottleneck()

	if curPB.BottleneckStart > 0  && genNum >= curPB.BottleneckStart + curPB.BottleneckGens {
		// We just stepped past our current Bottleneck element, so move to the next one
		curPB = pb.NextBottleneck()
	}
	config.Verbose(2, "Using bottleneck values: %v:%d:%d:%d:%d", curPB.GrowthRate, curPB.MaxPop, curPB.BottleneckStart, curPB.BottleneckPopSize, curPB.BottleneckGens)

	if curPB.BottleneckStart == 0 || genNum < curPB.BottleneckStart {
		// We are before the bottleneck so use our growth rate
		newTargetSize = uint32(math.Ceil(curPB.GrowthRate * float64(prevPop.TargetSize)))
	} else if genNum >= curPB.BottleneckStart && genNum < curPB.BottleneckStart + curPB.BottleneckGens {
		// We are in the bottleneck range
		newTargetSize = curPB.BottleneckPopSize
	} else {
		// The only way we can't get here is if this bottleneck gen range is <= previous bottleneck gen range
		log.Fatalf("Error in bottleneck at generation %d: the current bottleneck gen range is <= previous bottleneck gen range", genNum)
	}
	if curPB.MaxPop != 0 {
		newTargetSize = utils.MinUint32(newTargetSize, curPB.MaxPop) // do not want it exceeding the max pop
	}

	return newTargetSize
}

type Bottlenecks struct {
	Bottlenecks []Bottleneck
	CurrentIndex uint32
}

type Bottleneck struct {
	GrowthRate float64		// the pop growth rate before this bottleneck, 1.0 means no growth
	MaxPop uint32			// the max pop size before this bottleneck, 0 means no max
	BottleneckStart uint32		// the starting gen num, 0 means no more bottlenecks
	BottleneckPopSize uint32	// the pop size during the bottleneck time period
	BottleneckGens uint32		// the num gens the bottleneck will last, then we move on to the next Bottleneck element
}

// ParseMultipleBottlenecks parses the config value that is comma-separated 5-tuples growth-rate:max-pop:bottle-start:bottle-size:bottle-gens
// It returns the Bottlenecks iterable
func ParseMultipleBottlenecks(bottlenecksStr string) *Bottlenecks {
	errorStr := fmt.Sprintf("Error: if pop_growth_model==%s, then multiple_bottlenecks must be like: growth-rate:max-pop:bottle-start:bottle-size:bottle-gens,...", string(MULTI_BOTTLENECK_POPULATON_GROWTH))
	var bottlenecks Bottlenecks
	if strings.TrimSpace(bottlenecksStr) == "" { log.Fatal(errorStr) }
	tuples := strings.Split(bottlenecksStr, ",")
	for i, t := range tuples {
		parts := strings.Split(strings.TrimSpace(t), ":")	// get the 5 values of this tuple. Only the growth-rate is required, the rest can default if there are no more bottlenecks
		if len(parts) < 1 || len(parts) > 5 { log.Fatal(errorStr) }
		var bottle Bottleneck
		var tmpInt64 int64
		var err error
		bottle.GrowthRate, err = strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
		if err != nil  { log.Fatalf("Error parsing growth-rate in element %d of multiple_bottlenecks: %v", i+1, err) }
		if bottle.GrowthRate <= 0.0 { log.Fatalf("Error: growth-rate in multiple_bottlenecks must be > 0.0, not %v", bottle.GrowthRate) }

		if len(parts) >= 2 {
			tmpInt64, err = strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 32)
			if err != nil {
				log.Fatalf("Error parsing max-pop in element %d of multiple_bottlenecks: %v", i+1, err)
			}
			bottle.MaxPop = uint32(tmpInt64)
			if bottle.MaxPop < 0 {
				log.Fatalf("Error: max-pop in multiple_bottlenecks must be >= 0, not %d", bottle.MaxPop)
			}
		}

		if len(parts) >= 3 {
			tmpInt64, err = strconv.ParseInt(strings.TrimSpace(parts[2]), 10, 32)
			if err != nil {
				log.Fatalf("Error parsing bottleneck-start in element %d of multiple_bottlenecks: %v", i+1, err)
			}
			bottle.BottleneckStart = uint32(tmpInt64)
			if bottle.BottleneckStart < 0 {
				log.Fatalf("Error: bottleneck-start in multiple_bottlenecks must be >= 0, not %d", bottle.BottleneckStart)
			}
		}

		if len(parts) >= 4 {
			tmpInt64, err = strconv.ParseInt(strings.TrimSpace(parts[3]), 10, 32)
			if err != nil {
				log.Fatalf("Error parsing bottleneck-size in element %d of multiple_bottlenecks: %v", i+1, err)
			}
			bottle.BottleneckPopSize = uint32(tmpInt64)
			if bottle.BottleneckPopSize < 0 {
				log.Fatalf("Error: bottleneck-size in multiple_bottlenecks must be >= 0, not %d", bottle.BottleneckPopSize)
			}
		}

		if len(parts) >= 5 {
			tmpInt64, err = strconv.ParseInt(strings.TrimSpace(parts[4]), 10, 32)
			if err != nil {
				log.Fatalf("Error parsing bottleneck-gens in element %d of multiple_bottlenecks: %v", i+1, err)
			}
			bottle.BottleneckGens = uint32(tmpInt64)
			if bottle.BottleneckGens < 0 {
				log.Fatalf("Error: bottleneck-gens in multiple_bottlenecks must be >= 0, not %d", bottle.BottleneckGens)
			}
		}

		bottlenecks.Bottlenecks = append(bottlenecks.Bottlenecks, bottle)
	}
	return &bottlenecks
}

// CurrentBottleneck returns the current Bottleneck element.
func (pb *Bottlenecks) CurrentBottleneck() Bottleneck {
	return pb.Bottlenecks[pb.CurrentIndex]
}

// NextBottleneck returns the next Bottleneck element.
func (pb *Bottlenecks) NextBottleneck() Bottleneck {
	if pb.CurrentIndex >= uint32(len(pb.Bottlenecks) - 1) {
		// The index is already at the end, return a manufactured bottleneck element that indicates there are no more bottlenecks
		return Bottleneck{GrowthRate: 1.0} // the rest of the values being 0 indicate no more bottlenecks
	}
	pb.CurrentIndex++
	return pb.Bottlenecks[pb.CurrentIndex]
}

// PreSelectFitnessStats returns the mean geno fitness and std deviation
func (p *Population) PreSelectFitnessStats() (genoFitnessMean, genoFitnessVariance, genoFitnessStDev float64) {
	// Calc mean (average)
	for _, indRef := range p.IndivRefs {
		ind := indRef.Indiv
		genoFitnessMean += ind.GenoFitness
	}
	genoFitnessMean = genoFitnessMean / float64(p.GetCurrentSize())

	for _, indRef := range p.IndivRefs {
		ind := indRef.Indiv
		genoFitnessVariance += math.Pow(ind.GenoFitness-genoFitnessMean, 2)
	}
	genoFitnessVariance = genoFitnessVariance / float64(p.GetCurrentSize())
	genoFitnessStDev = math.Sqrt(genoFitnessVariance)
	return
}


// ReportDeadStats reports means of all the individuals that are being eliminated by selection
func (p *Population) ReportDeadStats() {
	elimVerboseLevel := uint32(4)            // level at which we will collect and print stats about dead/eliminated individuals
	if !config.IsVerbose(elimVerboseLevel) { return }
	var avgDel, avgNeut, avgFav, /*avgDelFit, avgFavFit,*/ avgFitness, minFitness, maxFitness float64 	// these are stats for dead/eliminated individuals
	minFitness = 99.0
	maxFitness = -99.0
	var numDead, numDel, numNeut, numFav uint32
	for _, indRef := range p.IndivRefs {
		ind := indRef.Indiv
		//todo: stop when i hit the 1st non-dead individual
		if ind.Dead {
			// This is a dead individual. Capture some stats before we overwrite it.
			numDead++
			avgFitness += ind.GenoFitness
			if ind.GenoFitness > maxFitness {
				maxFitness = ind.GenoFitness
			}
			if ind.GenoFitness < minFitness {
				minFitness = ind.GenoFitness
			}
			d, n, f := ind.GetMutationStats()
			numDel += d
			numNeut += n
			numFav += f
		}
	}

	// Calculate and print the elimination stats
	if numDead > 0 {
		avgFitness = avgFitness / float64(numDead)
		avgDel = float64(numDel) / float64(numDead)
		avgNeut = float64(numNeut) / float64(numDead)
		avgFav = float64(numFav) / float64(numDead)
	}
	config.Verbose(elimVerboseLevel, "Avgs of the %d indivs eliminated: avg fitness: %v, min fitness: %v, max fitness: %v, del: %v, neut: %v, fav: %v", numDead, avgFitness, minFitness, maxFitness, avgDel, avgNeut, avgFav)
}


// GetFitnessStats returns the average of all the individuals fitness levels, as well as the min and max, and total and mean mutations.
// Note: this function should only get stats that the individuals already have, because it is called in a minimal verbose level that is meant to be fast.
func (p *Population) GetFitnessStats() (float64, float64, float64, uint64, float64) {
	// See if we already calculated and cached the values
	if p.MeanFitness > 0.0 { return p.MeanFitness, p.MinFitness, p.MaxFitness, p.TotalNumMutations, p.MeanNumMutations }
	popSize := p.GetCurrentSize()
	if popSize == 0 { return 0.0, 0.0, 0.0, 0, 0.0 }
	p.MinFitness = 99.0
	p.MaxFitness = -99.0
	p.MeanFitness = 0.0
	p.TotalNumMutations = 0
	p.MeanNumMutations = 0.0
	for _, indRef := range p.IndivRefs {
		ind := indRef.Indiv
		p.MeanFitness += ind.GenoFitness
		if ind.GenoFitness > p.MaxFitness { p.MaxFitness = ind.GenoFitness }
		if ind.GenoFitness < p.MinFitness { p.MinFitness = ind.GenoFitness }
		p.TotalNumMutations += uint64(ind.NumMutations)
	}
	p.MeanFitness = p.MeanFitness / float64(popSize)
	p.MeanNumMutations = float64(p.TotalNumMutations) / float64(popSize)
	return p.MeanFitness, p.MinFitness, p.MaxFitness, p.TotalNumMutations, p.MeanNumMutations
}


// GetMutationStats returns the average number of deleterious, neutral, favorable mutations
func (p *Population) GetMutationStats() (float64, float64, float64 /*,  float64, float64*/) {
	// See if we already calculated and cached the values. Note: we only check deleterious, because fav and neutral could be 0
	if p.MeanNumDeleterious > 0 { return p.MeanNumDeleterious, p.MeanNumNeutral, p.MeanNumFavorable }
	popSize := float64(p.GetCurrentSize())
	if popSize == 0 { return 0.0, 0.0, 0.0 }
	p.MeanNumDeleterious=0.0;  p.MeanNumNeutral=0.0;  p.MeanNumFavorable=0.0

	// For each type of mutation, get the average fitness factor and number of mutation for every individual and combine them. Example: 20 @ .2 and 5 @ .4 = (20 * .2) + (5 * .4) / 25
	var delet, neut, fav uint64
	for _, indRef := range p.IndivRefs {
		ind := indRef.Indiv
		d, n, f := ind.GetMutationStats()
		delet += uint64(d)
		neut += uint64(n)
		fav += uint64(f)
	}
	p.MeanNumDeleterious = float64(delet) / popSize
	p.MeanNumNeutral = float64(neut) / popSize
	p.MeanNumFavorable = float64(fav) / popSize
	return p.MeanNumDeleterious, p.MeanNumNeutral, p.MeanNumFavorable
}


// GetInitialAlleleStats returns the average number of deleterious and favorable initial alleles
func (p *Population) GetInitialAlleleStats() (float64, /*float64,*/ float64) {
	// See if we already calculated and cached the values. Note: we only check deleterious, because fav and neutral could be 0
	if p.MeanNumDelAllele > 0 { return p.MeanNumDelAllele, p.MeanNumFavAllele }
	p.MeanNumDelAllele=0.0;  p.MeanNumFavAllele=0.0

	// For each type of allele, get the average fitness factor and number of alleles for every individual and combine them. Example: 20 @ .2 and 5 @ .4 = (20 * .2) + (5 * .4) / 25
	var delet, fav uint32
	for _, indRef := range p.IndivRefs {
		ind := indRef.Indiv
		d, f := ind.GetInitialAlleleStats()
		delet += d
		fav += f
	}
	size := float64(p.GetCurrentSize())
	if size > 0 {
		p.MeanNumDelAllele = float64(delet) / size
		p.MeanNumFavAllele = float64(fav) / size
	}
	return p.MeanNumDelAllele, p.MeanNumFavAllele
}


// ReportInitial prints out stuff at the beginning, usually headers for data files, or a summary of the run we are about to do
func (p *Population) ReportInitial() {
	// Report initial alleles if there are any
	initialVerboseLevel := uint32(1)            // level at which we will print population summary info at the end of the run
	if config.Cfg.Population.Num_contrasting_alleles > 0 && config.IsVerbose(initialVerboseLevel) {
		ad, af := p.GetInitialAlleleStats()
		log.Printf(" Indiv initial allele detail means: deleterious: %v, favorable: %v", ad, af)
	}

	if histWriter := config.FMgr.GetFile(config.HISTORY_FILENAME, p.TribeNum); histWriter != nil {
		// Write header for this file
		fmt.Fprintln(histWriter, "# Generation  Avg-deleterious Avg-neutral  Avg-favorable")
	}

	if fitWriter := config.FMgr.GetFile(config.FITNESS_FILENAME, p.TribeNum); fitWriter != nil {
		// Write header for this file
		fmt.Fprintln(fitWriter, "# Generation  Pop-size  Avg Offspring  Avg-fitness  Min-fitness  Max-fitness  Total Mutns  Mean Mutns  Noise")
	}
}


// Report prints out statistics of this population
func (p *Population) ReportEachGen(genNum uint32, lastGen bool, totalInterimTime, genTime float64, memUsed float32) {
	if p.Done { return }
	perGenMinimalVerboseLevel := uint32(1)            // level at which we will print only the info that is very quick to gather
	perGenVerboseLevel := uint32(2)            // level at which we will print population summary info each generation
	finalVerboseLevel := uint32(1)            // level at which we will print population summary info at the end of the run
	perGenIndSumVerboseLevel := uint32(3) 		// level at which we will print individuals summary info each generation
	finalIndSumVerboseLevel := uint32(2) // Note: if you change this value, change the verbose level used to calc the values in Mate(). Level at which we will print individuals summary info at the end of the run
	perGenIndDetailVerboseLevel := uint32(7)    // level at which we will print info about each individual each generation
	finalIndDetailVerboseLevel := uint32(6)    // level at which we will print info about each individual at the end of the run
	popSize := p.GetCurrentSize()

	if config.IsVerbose(perGenVerboseLevel) || (lastGen && config.IsVerbose(finalVerboseLevel)) {
		aveFit, minFit, maxFit, totalMutns, meanMutns := p.GetFitnessStats()
		log.Printf("Tribe: %d, Gen: %d, Time: %.4f, Gen time: %.4f, Mem: %.3f MB, Pop size: %v, Indiv mean fitness: %v, min fitness: %v, max fitness: %v, total num mutations: %v, mean num mutations: %v, Mean num offspring %v, noise: %v", p.TribeNum, genNum, totalInterimTime, genTime, memUsed, popSize, aveFit, minFit, maxFit, totalMutns, meanMutns, p.ActualAvgOffspring, p.EnvironNoise)
		if config.IsVerbose(perGenIndSumVerboseLevel) || (lastGen && config.IsVerbose(finalIndSumVerboseLevel)) {
			d, n, f := p.GetMutationStats()
			log.Printf(" Indiv mutation detail means: deleterious: %v, neutral: %v, favorable: %v, preselect fitness: %v, preselect fitness SD: %v", d, n, f, p.PreSelGenoFitnessMean, p.PreSelGenoFitnessStDev)
		}
	} else if config.IsVerbose(perGenMinimalVerboseLevel) {
		aveFit, minFit, maxFit, totalMutns, meanMutns := p.GetFitnessStats()		// this is much faster than p.GetMutationStats()
		log.Printf("Tribe: %d, Gen: %d, Time: %.4f, Gen time: %.4f, Mem: %.3f MB, Pop size: %v, Indiv mean fitness: %v, min fitness: %v, max fitness: %v, total num mutations: %v, mean num mutations: %v, Mean num offspring %v", p.TribeNum, genNum, totalInterimTime, genTime, memUsed, popSize, aveFit, minFit, maxFit, totalMutns, meanMutns, p.ActualAvgOffspring)
	}
	if config.IsVerbose(perGenIndDetailVerboseLevel) || (lastGen && config.IsVerbose(finalIndDetailVerboseLevel)) {
		log.Println(" Individual Detail:")
		for _, indRef := range p.IndivRefs {
			ind := indRef.Indiv
			ind.Report(lastGen)
		}
	}

	if histWriter := config.FMgr.GetFile(config.HISTORY_FILENAME, p.TribeNum); histWriter != nil {
		config.Verbose(5, "Writing to file %v", config.HISTORY_FILENAME)
		d, n, f := p.GetMutationStats()		// GetMutationStats() caches its values so it's ok to call it multiple times
		// If you change this line, you must also change the header in ReportInitial()
		fmt.Fprintf(histWriter, "%d  %v  %v  %v\n", genNum, d, n, f)
		//histWriter.Flush()  // <-- don't need this because we don't use a buffer for the file
		if lastGen {
			//todo: put summary stats in comments at the end of the file?
		}
	}

	if fitWriter := config.FMgr.GetFile(config.FITNESS_FILENAME, p.TribeNum); fitWriter != nil {
		config.Verbose(5, "Writing to file %v", config.FITNESS_FILENAME)
		aveFit, minFit, maxFit, totalMutns, meanMutns := p.GetFitnessStats()		// GetFitnessStats() caches its values so it's ok to call it multiple times
		// If you change this line, you must also change the header in ReportInitial()
		fmt.Fprintf(fitWriter, "%d  %d  %v  %v  %v  %v  %v  %v  %v\n", genNum, popSize, p.ActualAvgOffspring, aveFit, minFit, maxFit, totalMutns, meanMutns, p.EnvironNoise)
		//histWriter.Flush()  // <-- don't need this because we don't use a buffer for the file
		if lastGen {
			//todo: put summary stats in comments at the end of the file?
		}
	}

	// Note: Species.ReportEachGen() runs Population.CountAlleles()
}

func (p *Population) CountAlleles(genNum uint32, lastGen bool) {
	//if p.Done { return }  // even if a tribe went extinct, we might still be interested in its allele plots, as long as its pop > 0
	if (config.FMgr.IsDir(config.ALLELE_BINS_DIRECTORY) || config.FMgr.IsDir(config.NORMALIZED_ALLELE_BINS_DIRECTORY) || config.FMgr.IsDir(config.DISTRIBUTION_DEL_DIRECTORY) || config.FMgr.IsDir(config.DISTRIBUTION_FAV_DIRECTORY)) && (lastGen || (config.Cfg.Computation.Plot_allele_gens > 0 && (genNum % config.Cfg.Computation.Plot_allele_gens) == 0)) {
		popSize := p.GetCurrentSize()
		if popSize == 0 { return }
		alleles := p.getAlleles(genNum, popSize, lastGen)
		p.outputAlleleBins(genNum, popSize, lastGen, alleles)
		p.outputAlleleDistribution(genNum, popSize, lastGen, alleles)
	}
}

type Buckets struct {
	Generation uint32 `json:"generation"`
	Bins []uint32 `json:"bins"`
	Deleterious []uint32 `json:"deleterious"`
	Neutral []uint32 `json:"neutral"`
	Favorable []uint32 `json:"favorable"`
	DelInitialAlleles []uint32 `json:"delInitialAlleles"`
	FavInitialAlleles []uint32 `json:"favInitialAlleles"`
}

type NormalizedBuckets struct {
	Generation uint32 `json:"generation"`
	//Bins []float64 `json:"bins"`
	Bins []uint32 `json:"bins"`
	Deleterious []float64 `json:"deleterious"`
	Neutral []float64 `json:"neutral"`
	Favorable []float64 `json:"favorable"`
	DelInitialAlleles []float64 `json:"delInitialAlleles"`
	FavInitialAlleles []float64 `json:"favInitialAlleles"`
}

type DistributionBuckets struct {
	Generation uint32 `json:"generation"`
	BinMidpointFitness []float64 `json:"binmidpointfitness"`
	Recessive []float64 `json:"recessive"`
	Dominant []float64 `json:"dominant"`
}


// getAlleles gathers all of the alleles in this generation and returns them
func (p *Population) getAlleles(genNum, popSize uint32, lastGen bool) (alleles *dna.AlleleCount) {
	config.Verbose(1, "Counting alleles for tribe %d", p.TribeNum)
	// Free up some memory, because this is going to take a lot
	if lastGen && config.Cfg.Computation.Allele_count_gc_interval > 0 {
		debug.SetGCPercent(-1) 		// if force_gc=false we didn't do this earlier
	}

	// Count the alleles from all individuals. We end up with maps of mutation ids and the number of times each occurred
	alleles = dna.AlleleCountFactory() 		// as we count, the totals are gathered in this struct
	gcInterval := config.Cfg.Computation.Allele_count_gc_interval
	if gcInterval > 0 && gcInterval < 100 {
		// Interpret this as a %, with a min and max bound
		gcInterval = uint32(float32(p.GetCurrentSize() * gcInterval) / 100.0)
		gcInterval = utils.MaxUint32( utils.MinUint32(gcInterval, 500), 100 )
	}
	for i := range p.IndivRefs {
		p.IndivRefs[i].Indiv.CountAlleles(alleles)

		// Counting the alleles takes a lot of memory when there are a lot of mutations. We are concerned that after doing the whole
		// run, we could blow the memory limit counting the alleles and lose all of the run results. So if this is the last gen
		// we don't need the individuals after we have counted them, so nil the reference to them so GC can reclaim.
		//utils.Measure.CheckAmountMemoryUsed()
		if lastGen {
			p.IndivRefs[i].Indiv = nil
			if gcInterval > 0 && (i % int(gcInterval)) == 0 { utils.CollectGarbage() }
		}

		if i != 0 && gcInterval > 0 && (i % int(gcInterval)) == 0 { config.Verbose(1, "Counted alleles in %d individuals", i) }
	}
	return
}


// outputAlleleBins calculates the bins using the alleles struct, and outputs them to a file
func (p *Population) outputAlleleBins(genNum, popSize uint32, lastGen bool, alleles *dna.AlleleCount) {
	//var deleteriousDom, deleteriousRec, neutral, favorableDom, favorableRec, delAllele, favAllele uint32

	// Write the plot file for each type of mutation/allele
	bucketCount := uint32(100)		// we could put this in the config file if we need to
	bucketJson := &Buckets{}

	bucketJson.Generation = genNum

	bucketJson.Bins = make([]uint32, bucketCount)
	for i := range bucketJson.Bins {
		bucketJson.Bins[i] = uint32(i) + 1
	}

	bucketJson.Deleterious = make([]uint32, bucketCount)
	deleteriousDom, delDomFitness := fillBuckets(alleles.DeleteriousDom, popSize, bucketCount, bucketJson.Deleterious)
	deleteriousRec, delRecFitness := fillBuckets(alleles.DeleteriousRec, popSize, bucketCount, bucketJson.Deleterious)

	// Note: we do this even when there are no neutrals, because the plotting software needs all 0's in that case
	bucketJson.Neutral = make([]uint32, bucketCount)
	neutral, _ := fillBuckets(alleles.Neutral, popSize, bucketCount, bucketJson.Neutral)

	bucketJson.Favorable = make([]uint32, bucketCount)
	favorableDom, favDomFitness := fillBuckets(alleles.FavorableDom, popSize, bucketCount, bucketJson.Favorable)
	favorableRec, favRecFitness := fillBuckets(alleles.FavorableRec, popSize, bucketCount, bucketJson.Favorable)

	bucketJson.DelInitialAlleles = make([]uint32, bucketCount)
	delAllele, delAlleleFitness := fillBuckets(alleles.DelInitialAlleles, popSize, bucketCount, bucketJson.DelInitialAlleles)
	bucketJson.FavInitialAlleles = make([]uint32, bucketCount)
	favAllele, favAlleleFitness := fillBuckets(alleles.FavInitialAlleles, popSize, bucketCount, bucketJson.FavInitialAlleles)

	// Output allele totals stats that we collected during fillBuckets()
	totalMutns := deleteriousDom + deleteriousRec + neutral + favorableDom + favorableRec + delAllele + favAllele
	countingStr := "counting duplicates"
	if !config.Cfg.Computation.Count_duplicate_alleles {
		countingStr = "filtering out duplicates"
	}
	var delFitness, favFitness float64
	deleterious := deleteriousDom + deleteriousRec
	if deleterious > 0 { delFitness = (delDomFitness+delRecFitness)/float64(deleterious) }
	favorable := favorableDom + favorableRec
	if favorable > 0 { favFitness = (favDomFitness+favRecFitness)/float64(favorable) }
	if delAllele > 0 { delAlleleFitness = delAlleleFitness/float64(delAllele) }
	if favAllele > 0 { favAlleleFitness = favAlleleFitness/float64(favAllele) }
	config.Verbose(1, "---------")
	config.Verbose(1, "Total tracked allele stats (%s): total alleles: %d, deleterious: %d, delFitness: %v, neutral: %d, favorable: %d, favFitness: %v, del initial: %d, del initial fitness: %v, fav initial: %d, fav initial fitness: %v",
		countingStr, totalMutns, deleterious, delFitness, neutral, favorable, favFitness, delAllele, delAlleleFitness, favAllele, favAlleleFitness)

	// Collect unique allele stats
	firstBinMutns := uint64(bucketJson.Deleterious[0] + bucketJson.Neutral[0] + bucketJson.Favorable[0])
	firstBinAlleles := uint64(bucketJson.DelInitialAlleles[0] + bucketJson.FavInitialAlleles[0])
	lastIndex := bucketCount-1
	lastBinMutns := uint64(bucketJson.Deleterious[lastIndex] + bucketJson.Neutral[lastIndex] + bucketJson.Favorable[lastIndex])
	lastBinAlleles := uint64(bucketJson.DelInitialAlleles[lastIndex] + bucketJson.FavInitialAlleles[lastIndex])
	// FYI, the total of these 2 values is equal to the sum of all buckets in bucketJson
	totalUniqueMutns := uint64(len(alleles.DeleteriousDom) + len(alleles.DeleteriousRec) + len(alleles.Neutral) + len(alleles.FavorableDom) + len(alleles.FavorableRec))
	totalUniqueAlleles := uint64(len(alleles.DelInitialAlleles) + len(alleles.FavInitialAlleles))
	polyMutns := totalUniqueMutns - uint64(firstBinMutns + lastBinMutns)
	polyAlleles := totalUniqueAlleles - uint64(firstBinAlleles + lastBinAlleles)
	config.Verbose(1, "Allele bin stats: rare alleles (0-1%%) total/mutns/alleles: %d/%d/%d, polymorphic alleles (1-99%%) total/mutns/alleles: %d/%d/%d, fixed alleles (99-100%%) total/mutns/alleles: %d/%d/%d",
		firstBinMutns+firstBinAlleles, firstBinMutns, firstBinAlleles, polyMutns+polyAlleles, polyMutns, polyAlleles, lastBinMutns+lastBinAlleles, lastBinMutns, lastBinAlleles)

	if config.Cfg.Computation.Omit_first_allele_bin {
		// Shift all slices 1 to the left. This will affect both the allele bin output and the normalized output
		bucketJson.Bins = bucketJson.Bins[1:]
		bucketJson.Deleterious = bucketJson.Deleterious[1:]
		bucketJson.Neutral = bucketJson.Neutral[1:]
		bucketJson.Favorable = bucketJson.Favorable[1:]
		bucketJson.DelInitialAlleles = bucketJson.DelInitialAlleles[1:]
		bucketJson.FavInitialAlleles = bucketJson.FavInitialAlleles[1:]
	}

	fileName := fmt.Sprintf("%08d.json", genNum)

	if config.FMgr.IsDir(config.ALLELE_BINS_DIRECTORY) {
		newJson, err := json.Marshal(bucketJson)
		if err != nil { log.Fatalf("error marshaling allele bins to json: %v", err)	}
		//log.Printf("=== writing to %s, %s, %v", config.ALLELE_BINS_DIRECTORY, fileName, p.TribeNum)
		if alleleWriter := config.FMgr.GetDirFile(config.ALLELE_BINS_DIRECTORY, fileName, p.TribeNum); alleleWriter != nil {
			//log.Printf("=== really writing to %s, %s, %v", config.ALLELE_BINS_DIRECTORY, fileName, p.TribeNum)
			if _, err := alleleWriter.Write(newJson); err != nil { log.Fatalf("error writing alleles to %v: %v", fileName, err) }
			config.FMgr.CloseDirFile(config.ALLELE_BINS_DIRECTORY, fileName, p.TribeNum)
		}
	}

	if config.FMgr.IsDir(config.NORMALIZED_ALLELE_BINS_DIRECTORY) {
		p.outputNormalizedAlleleBins(bucketJson, bucketCount, genNum, fileName)
	}
}

// outputNormalizedAlleleBins uses the absolute data gathered in outputAlleleBins() and normalizes all of the bin counts (by dividing them by the total number of alleles)
func (p *Population) outputNormalizedAlleleBins(bucketJson *Buckets, bucketCount uint32, genNum uint32, fileName string) {
	normalizedBucketCount := bucketCount / 2
	omitStr := "including first bin"
	if config.Cfg.Computation.Omit_first_allele_bin {
		// The slices in bucketJson have already be shifted left, so decrement our count so we effectively show bins 1-49, instead of 0-49
		normalizedBucketCount--
		omitStr = "excluding first bin"
	}

	// Normalize by dividing by the number of alleles we are plotting (the minor alleles, omitting the 1st bin if Omit_first_allele_bin==true
	var minorAlleleTotal, minorMutns, minorAlleles uint64
	for i := uint32(0); i < normalizedBucketCount; i++ {
		// These buckets are already adjusted for Omit_first_allele_bin
		minorMutns += uint64(bucketJson.Deleterious[i])
		minorMutns += uint64(bucketJson.Neutral[i])
		minorMutns += uint64(bucketJson.Favorable[i])
		minorAlleles += uint64(bucketJson.DelInitialAlleles[i])
		minorAlleles += uint64(bucketJson.FavInitialAlleles[i])
	}
	minorAlleleTotal = minorMutns + minorAlleles
	config.Verbose(1, "Unique minor alleles (%s): total: %d, mutations: %d, initial alleles: %d", omitStr, minorAlleleTotal, minorMutns, minorAlleles)
	config.Verbose(1, "---------")

	normalizedBucketJson := &NormalizedBuckets{}

	normalizedBucketJson.Generation = genNum

	normalizedBucketJson.Bins = make([]uint32, normalizedBucketCount)
	for i := range normalizedBucketJson.Bins {
		normalizedBucketJson.Bins[i] = uint32(i + 1)
	}

	normalizedBucketJson.Deleterious = make([]float64, normalizedBucketCount)
	for i := uint32(0); i < normalizedBucketCount; i++ {
		normalizedBucketJson.Deleterious[i] = float64(bucketJson.Deleterious[i]) / float64(minorAlleleTotal)
	}

	normalizedBucketJson.Neutral = make([]float64, normalizedBucketCount)
	for i := uint32(0); i < normalizedBucketCount; i++ {
		normalizedBucketJson.Neutral[i] = float64(bucketJson.Neutral[i]) / float64(minorAlleleTotal)
	}

	normalizedBucketJson.Favorable = make([]float64, normalizedBucketCount)
	for i := uint32(0); i < normalizedBucketCount; i++ {
		normalizedBucketJson.Favorable[i] = float64(bucketJson.Favorable[i]) / float64(minorAlleleTotal)
	}

	normalizedBucketJson.DelInitialAlleles = make([]float64, normalizedBucketCount)
	for i := uint32(0); i < normalizedBucketCount; i++ {
		normalizedBucketJson.DelInitialAlleles[i] = float64(bucketJson.DelInitialAlleles[i]) / float64(minorAlleleTotal)
	}

	normalizedBucketJson.FavInitialAlleles = make([]float64, normalizedBucketCount)
	for i := uint32(0); i < normalizedBucketCount; i++ {
		normalizedBucketJson.FavInitialAlleles[i] = float64(bucketJson.FavInitialAlleles[i]) / float64(minorAlleleTotal)
	}

	newJson, err := json.Marshal(normalizedBucketJson)
	if err != nil { log.Fatalf("error marshaling normalized allele bins to json: %v", err) }

	if alleleWriter := config.FMgr.GetDirFile(config.NORMALIZED_ALLELE_BINS_DIRECTORY, fileName, p.TribeNum); alleleWriter != nil {
		if _, err := alleleWriter.Write(newJson); err != nil { log.Fatalf("error writing alleles to %v: %v", fileName, err) }
		config.FMgr.CloseDirFile(config.NORMALIZED_ALLELE_BINS_DIRECTORY, fileName, p.TribeNum)
	}
}


// fillBuckets takes the number of occurrences of each mutation id, determines which bucket it belongs in, and adds 1 to that bucket
func fillBuckets(counts map[uint64]dna.Allele, popSize uint32, bucketCount uint32, buckets []uint32) (totalMutns uint64, totalFitness float64) {
	poolSize := float64(2 * popSize)
	if !config.Cfg.Computation.Count_duplicate_alleles { poolSize = float64(popSize)}	// in this case, each allele count is a measure of how many individuals it occurred in

	for _, count := range counts {
		totalMutns += uint64(count.Count)
		totalFitness += float64(count.FitnessEffect) * float64(count.Count)
		percentage := float64(count.Count) / poolSize
		var i uint32
		floati := percentage * float64(bucketCount)

		//i = uint32(floati)
		//if i == bucketCount { i = bucketCount - 1 }

		/* I think this is the more appropriate way, but the simple truncation in the 2 lines above is what mendel-f90/bucket brigade does... */
		// At this point, if we just converted floati to uint32 (by truncating), index i would contain all float values: index <= floati < index+1
		// But we really want the indexes to contain: index < floati <= index+1
		// Because remember that when we output the buckets numbers into the file, we add 1 to the index of the bucket, so e.g. bucket 5 (index 4 here) will contain: 4 < count <= 5
		// (The issue is does a count that is exactly 5% end up in bucket 5 or 6. I think it should go in bucket 5.)
		// This also handles correctly the case in which the mutation is in every single individual. The mendel-f90/bucket brigade approach would put that in index 100, which doesn't exist.
		const roundingError = 0.000000000001
		trunci := math.Trunc(floati + roundingError)
		if floati > trunci - roundingError && floati < trunci + roundingError {
			i = uint32(trunci) - 1
		} else {
			i = uint32(floati)
		}


		// The way the calcs above are done, neither of these 2 cases should ever actually happen, but just a safeguard...
		if i < 0 {
			log.Printf("Warning: bucket index %d is out of range, putting it back in range.", i)
			i = 0
		} else if i >= bucketCount {
			if !config.Cfg.Computation.Count_duplicate_alleles { log.Printf("Warning: bucket index %d is out of range, putting it back in range.", i) }
			// else we expect this
			i = bucketCount - 1
		}

		buckets[i] += 1
	}

	return
}


// makeAndFillIndivRefs fills in the p.IndivRefs array from all of the p.Part.Indivs array
func (p *Population) makeAndFillIndivRefs() {
	// Find the total num of individuals so we can initialize the refs array
	size := 0
	for _, part := range p.Parts { size += int(part.GetCurrentSize()) }
	if cap(p.IndivRefs) < size {
		p.IndivRefs = make([]IndivRef, size)
	}
	// else this is a recycled pop and the IndivRefs was big enough

	// Now populate the refs array
	irIndex := 0
	for _, part := range p.Parts {
		for j := range part.Indivs {
			p.IndivRefs[irIndex].Indiv = part.Indivs[j]
			part.Indivs[j] = nil    // eliminate this reference to the individual so garbage collection can delete the individual as soon as we use and eliminate the reference in IndivRefs in Mate() of next gen
			irIndex++
		}
	}
}


// sortIndexByFitness sorts the references to the individuals (p.IndivRefs) according to the individual's fitness (in ascending order)
func (p *Population) sortIndexByPhenoFitness() {
	sort.Sort(ByFitness(p.IndivRefs)) 		// sort the p.IndivRefs according to fitness

	// Output the fitnesses to check them, if verbosity is high enough
	if config.IsVerbose(9) {
		fitSlice := make([]float64, len(p.IndivRefs)) 	// create an array of the sorted individual fitness values so we can print them compactly
		for i,ind := range p.IndivRefs { fitSlice[i] = ind.Indiv.PhenoFitness
		}
		config.Verbose(9, "fitSlice: %v", fitSlice)
	}
}


// outputAlleleDistribution computes the distribution of alleles according to the fitness.
func (p *Population) outputAlleleDistribution(genNum, popSize uint32, lastGen bool, alleles *dna.AlleleCount) {
	if !(config.FMgr.IsDir(config.DISTRIBUTION_DEL_DIRECTORY) || config.FMgr.IsDir(config.DISTRIBUTION_FAV_DIRECTORY)) { return }

	/*
	Taken from diagnostics.diagnostics_mutn_bins_plot() - lines 291-793, to produce file 8, .dst, distribution of accumulated del/fav mutns
	For reference, in dmutn(i,j,k) and fmutn(i,j,k):
	  i: 1 is the num of mutns in that individual, rest are the mutns in that indiv:
	  j: 1 (dad) or 2 (mom)
	  k: which individual
	  mutn value:
	    recessive (-) or dominant (+)
	    which linkage block the mutation resides in: abs(mutn)/lb_modulo
	    the fitness effect of the mutation: mod(abs(mutn),lb_modulo) / lb_modulo
	*/

	// This computes the data for distribution of alleles according to the fitness, files .deldst and .favdst
	// Use same variable names as mendel-f90 to make it easier to port those formulas
	logn := math.Log // can't use log() because there is a package named that
	exp := math.Exp
	pow := math.Pow
	//abs := math.Abs
	//current_pop_size := int(p.GetCurrentSize())
	//mutn_sum := float64(p.TotalNumMutations)   // a comment in diagnostices.f90 says this should be the expected number of mutns w/o selection
	mutn_sum := float64(p.GetCurrentSize() * genNum) * config.Cfg.Mutations.Mutn_rate
	frac_fav_mutn := config.Cfg.Mutations.Frac_fav_mutn
	tracking_threshold := utils.MaxFloat64(1.0/config.Cfg.Mutations.Genome_size, float64(config.Cfg.Computation.Tracking_threshold))
	max_fav_fitness_gain := config.Cfg.Mutations.Max_fav_fitness_gain
	alpha_del := config.Computed.Alpha_del
	alpha_fav := config.Computed.Alpha_fav
	gamma_del := config.Computed.Gamma_del
	gamma_fav := config.Computed.Gamma_fav
	//del_scale := config.Computed.Del_scale

	// Compute the bin widths
	del_bin_width := -logn(tracking_threshold) / 50
	var fav_bin_width float64
	if max_fav_fitness_gain > 0.0 {
		fav_bin_width = -logn(tracking_threshold / max_fav_fitness_gain) / 50
	} else {
		fav_bin_width = del_bin_width
	}
	//fmt.Printf("DEBUG: del_bin_width=%v, alpha_del=%v, gamma_del=%v\n", del_bin_width, alpha_del, gamma_del)
	//fmt.Printf("DEBUG: fav_bin_width=%v, alpha_fav=%v, gamma_fav=%v\n", fav_bin_width, alpha_fav, gamma_fav)

	// Two differences between diagnostices.f90 and here: they put del info in the 1st 50 elements of the arrays and fav info in the 2nd 50,
	// and they have a 2nd dimension to the array to hold both recessive and dominant info. We use separate arrays for all 4 of those aspects.
	var del_refr_bins, fav_refr_bins [51]float64	// we are ignoring the 0th element to match fortran
	x0 := 0.0
	y0 := 0.0
	for k := 1; k <= 50; k++ {
		x1 := pow(del_bin_width * float64(k) / alpha_del, 1. / gamma_del)
		del_refr_bins[k] = (1. - frac_fav_mutn) * mutn_sum * (x1 - x0)
		y1 := pow(fav_bin_width * float64(k) / alpha_fav, 1. / gamma_fav)
		fav_refr_bins[k] = frac_fav_mutn * mutn_sum * (y1 - y0)
		x0 = x1
		y0 = y1
	}

	// Compute statistics on deleterious and favorable mutations
	// In diagnostices.f90, fitness_bins(k,l): k<=50 is del, k>50 is fav, l=1 is rec, l=2 is dom
	del_rec_fitness_bins := make([]float64, 51)	// we are ignoring the 0th element to match fortran
	del_dom_fitness_bins := make([]float64, 51)
	fav_rec_fitness_bins := make([]float64, 51)
	fav_dom_fitness_bins := make([]float64, 51)
	//fmt.Println("DEBUG: Deleterious Recessive:")
	//fillInFitnessBins(alleles.DeleteriousRec, alpha_del, gamma_del, del_bin_width, del_rec_fitness_bins)
	fillInFitnessBins(alleles.DeleteriousRec, 0.0, del_bin_width, del_rec_fitness_bins)
	//fmt.Println("DEBUG: Deleterious Dominant:")
	//fillInFitnessBins(alleles.DeleteriousDom, alpha_del, gamma_del, del_bin_width, del_dom_fitness_bins)
	fillInFitnessBins(alleles.DeleteriousDom, 0.0, del_bin_width, del_dom_fitness_bins)
	//fmt.Println("DEBUG: Favorable Recessive:")
	//fillInFitnessBins(alleles.FavorableRec, alpha_fav, gamma_fav, fav_bin_width, fav_rec_fitness_bins)
	fillInFitnessBins(alleles.FavorableRec, max_fav_fitness_gain, fav_bin_width, fav_rec_fitness_bins)
	//fmt.Println("DEBUG: Favorable Dominant:")
	//fillInFitnessBins(alleles.FavorableDom, alpha_fav, gamma_fav, fav_bin_width, fav_dom_fitness_bins)
	fillInFitnessBins(alleles.FavorableDom, max_fav_fitness_gain, fav_bin_width, fav_dom_fitness_bins)

	// Compute fitness values for bin boundaries and bin centers
	var del_bin_fitness, /*del_bin_fitness_boxwidth,*/ del_bin_fitness_midpoint, fav_bin_fitness, /*fav_bin_fitness_boxwidth,*/ fav_bin_fitness_midpoint [52]float64	// we are ignoring the 0th element to match fortran
	for k := 1; k <= 51; k++ {
		del_bin_fitness[k] = exp(-del_bin_width * float64(k - 1))
		if k > 1 {
			//del_bin_fitness_boxwidth[k - 1] = abs(del_bin_fitness[k] - del_bin_fitness[k-1])    // <- this doesn't seem to actually be used for the plots
			del_bin_fitness_midpoint[k - 1] = (del_bin_fitness[k] + del_bin_fitness[k-1]) / 2.
		}
	}
	for k := 1; k <= 51; k++ {
		fav_bin_fitness[k] = max_fav_fitness_gain * exp(-fav_bin_width * float64(k - 1))
		if k > 1 {
			//fav_bin_fitness_boxwidth[k - 1] = abs(fav_bin_fitness[k] - fav_bin_fitness[k-1])
			fav_bin_fitness_midpoint[k - 1] = (fav_bin_fitness[k] + fav_bin_fitness[k-1]) / 2.
		}
	}

	// Normalize the binned mutations by the reciprocal of the expected number of mutations per bin in the absence of selection
	x := 1. - config.Cfg.Mutations.Fraction_neutral
	if x == 0 { x = 1. }	// don't scale data if fraction_neutral = 1
	fraction_recessive := config.Cfg.Mutations.Fraction_recessive
	for k := 1; k <= 50; k++ {
		// Deleterious
		if del_refr_bins[k] > 0. && fraction_recessive > 0.{
			del_rec_fitness_bins[k] = del_rec_fitness_bins[k] / (fraction_recessive * del_refr_bins[k]) / x
		} else {
			del_rec_fitness_bins[k] = 0.
		}
		if del_refr_bins[k] > 0. && fraction_recessive < 1.{
			del_dom_fitness_bins[k] = del_dom_fitness_bins[k] / ((1. - fraction_recessive) * del_refr_bins[k]) / x
		} else {
			del_dom_fitness_bins[k] = 0.
		}

		// Favorable
		if fav_refr_bins[k] > 0. && fraction_recessive > 0.{
			fav_rec_fitness_bins[k] = fav_rec_fitness_bins[k] / (fraction_recessive * fav_refr_bins[k]) / x
		} else {
			fav_rec_fitness_bins[k] = 0.
		}
		if fav_refr_bins[k] > 0. && fraction_recessive < 1.{
			fav_dom_fitness_bins[k] = fav_dom_fitness_bins[k] / ((1. - fraction_recessive) * fav_refr_bins[k]) / x
		} else {
			fav_dom_fitness_bins[k] = 0.
		}
	}

	// For deleterious perform 3 iterations of smoothing on the fitness_bin values using a three-point average
	var rec_work, dom_work [51]float64
	for i := 1; i <= 3; i++ {
		fm1 := del_rec_fitness_bins[1]
		fm2 := del_dom_fitness_bins[1]
		for k := 2; k <= 49; k++ {
			av1 := del_rec_fitness_bins[k] + 0.5 * (fm1 + del_rec_fitness_bins[k+1])
			fm1 = del_rec_fitness_bins[k]
			rec_work[k] = 0.5 * av1
			av2 := del_dom_fitness_bins[k] + 0.5 * (fm2 + del_dom_fitness_bins[k+1])
			fm2 = del_dom_fitness_bins[k]
			dom_work[k] = 0.5 * av2
		}
		del_rec_fitness_bins[50] = 0.5 * (del_rec_fitness_bins[49] + del_rec_fitness_bins[50])
		del_dom_fitness_bins[50] = 0.5 * (del_dom_fitness_bins[49] + del_dom_fitness_bins[50])
		for k := 2; k <= 49; k++ {
			del_rec_fitness_bins[k] = rec_work[k]
			del_dom_fitness_bins[k] = dom_work[k]
		}
	}

	// For favorable distribution, limit maximum to a value of 100. To increase the smoothness, iterate the smoothing two times.
	for k := 1; k <= 50; k++ {
		fav_rec_fitness_bins[k] = utils.MinFloat64(100., fav_rec_fitness_bins[k])
		fav_dom_fitness_bins[k] = utils.MinFloat64(100., fav_dom_fitness_bins[k])
	}
	for i := 1; i <= 2; i++ {
		fm1 := fav_rec_fitness_bins[1]
		fm2 := fav_dom_fitness_bins[1]
		for k := 2; k <= 49; k++ {
			av1 := fav_rec_fitness_bins[k] + 0.5*(fm1+fav_rec_fitness_bins[k+1])
			fm1 = fav_rec_fitness_bins[k]
			rec_work[k] = 0.5 * av1
			av2 := fav_dom_fitness_bins[k] + 0.5*(fm2+fav_dom_fitness_bins[k+1])
			fm2 = fav_dom_fitness_bins[k]
			dom_work[k] = 0.5 * av2
		}
		fav_rec_fitness_bins[50] = 0.5 * (fav_rec_fitness_bins[49] + fav_rec_fitness_bins[50])
		fav_dom_fitness_bins[50] = 0.5 * (fav_dom_fitness_bins[49] + fav_dom_fitness_bins[50])
		for k := 2; k <= 49; k++ {
			fav_rec_fitness_bins[k] = rec_work[k]
			fav_dom_fitness_bins[k] = dom_work[k]
		}
	}

	// Output the deleterious and favorable distribution files
	delFileName := fmt.Sprintf("%08d.json", genNum)
	favFileName := fmt.Sprintf("%08d.json", genNum)
	//config.Verbose(1, "Writing %v and %v", delFileName, favFileName)
	if alleleWriter := config.FMgr.GetDirFile(config.DISTRIBUTION_DEL_DIRECTORY, delFileName, p.TribeNum); alleleWriter != nil {
		defer config.FMgr.CloseDirFile(config.DISTRIBUTION_DEL_DIRECTORY, delFileName, p.TribeNum)
		bucketJson := &DistributionBuckets{Generation:genNum}
		bucketJson.BinMidpointFitness = make([]float64, 50)
		bucketJson.Recessive = make([]float64, 50)
		bucketJson.Dominant = make([]float64, 50)
		//fmt.Fprintf(alleleWriter, "# generation = %d\n", genNum)
		//fmt.Fprintln(alleleWriter, "# bin_fitness   recessive  dominant   box_width")
		for k := 1; k <= 50; k++ {
			bucketJson.BinMidpointFitness[k-1] = del_bin_fitness_midpoint[k]
			bucketJson.Recessive[k-1] = del_rec_fitness_bins[k]
			bucketJson.Dominant[k-1] = del_dom_fitness_bins[k]
			//fmt.Fprintf(alleleWriter, "%v  %v  %v  %v\n", del_bin_fitness_midpoint[k], del_rec_fitness_bins[k], del_dom_fitness_bins[k], del_bin_fitness_boxwidth[k])
		}
		newJson, err := json.Marshal(bucketJson)
		if err != nil { log.Fatalf("error marshaling allele distribution bins to json: %v", err)	}
		if _, err := alleleWriter.Write(newJson); err != nil { log.Fatalf("error writing alleles to %v: %v", delFileName, err) }
	}
	if alleleWriter := config.FMgr.GetDirFile(config.DISTRIBUTION_FAV_DIRECTORY, favFileName, p.TribeNum); alleleWriter != nil {
		defer config.FMgr.CloseDirFile(config.DISTRIBUTION_FAV_DIRECTORY, favFileName, p.TribeNum)
		bucketJson := &DistributionBuckets{Generation:genNum}
		bucketJson.BinMidpointFitness = make([]float64, 50)
		bucketJson.Recessive = make([]float64, 50)
		bucketJson.Dominant = make([]float64, 50)
		//fmt.Fprintf(alleleWriter, "# generation = %d\n", genNum)
		//fmt.Fprintln(alleleWriter, "# bin_fitness   recessive  dominant   box_width  fav_refr_bins")
		for k := 1; k <= 50; k++ {
			bucketJson.BinMidpointFitness[k-1] = fav_bin_fitness_midpoint[k]
			bucketJson.Recessive[k-1] = fav_rec_fitness_bins[k]
			bucketJson.Dominant[k-1] = fav_dom_fitness_bins[k]
			//fmt.Fprintf(alleleWriter, "%v  %v  %v  %v %v\n", fav_bin_fitness_midpoint[k], fav_rec_fitness_bins[k], fav_dom_fitness_bins[k], fav_bin_fitness_boxwidth[k], fav_refr_bins[k])
		}
		newJson, err := json.Marshal(bucketJson)
		if err != nil { log.Fatalf("error marshaling allele distribution bins to json: %v", err)	}
		if _, err := alleleWriter.Write(newJson); err != nil { log.Fatalf("error writing alleles to %v: %v", favFileName, err) }
	}

	//todo: Compute the current values of the selection thresholds, file mendel.thr. This is a single file for the whole
	// run, where each row is for a different generation when this function is run.
	// Compute estimate for the current deleterious dominant threshold
	/*
	k := 1
	k0 := 0
	sum := (1. - fraction_recessive) * del_refr_bins[50]
	del_dom_thres := 0.0
	*/

}

func fillInFitnessBins(alleles map[uint64]dna.Allele, max_fav_fitness_gain, bin_width float64, fitness_bins []float64) {
	abs := math.Abs
	logn := math.Log
	//debugI := 1
	for _, allele := range alleles {
		/* diagnostics.f90, lines 331-393 does:
            x = mod(abs(dmutn(j,1,i)),lb_modulo)*del_scale
            d = alpha_del*x**gamma_del
		  but mutation.f90 decode_mutn_del() also does:
		    -dexp(-d)
		  to get the mutn fitness. So for us to get from our fitness f back to diagnostic.f90's value d:
		    f = -dexp(-d)
		    -f = dexp(-d)
		    logn(-f) = -d
		    -logn(-f) = d
		  decode_fitness_fav() does:
		    max_fav_fitness_gain*dexp(-d)
		  So for us to get from our fav fitness f back to diagnostic.f90's value d:
		    f = max_fav_fitness_gain*dexp(-d)
		    f / max_fav_fitness_gain = dexp(-d)
		    logn(f / max_fav_fitness_gain) = -d
		    -logn(f / max_fav_fitness_gain) = d
		 */
		var d float64
		if max_fav_fitness_gain <= 0.0 {		// deleterious
			d = -logn(abs(float64(allele.FitnessEffect)))
		} else {								// favorable
			d = -logn(float64(allele.FitnessEffect) / max_fav_fitness_gain)
		}
		k := 1 + int(d/bin_width)
		//if debugI <= 40 { fmt.Printf("DEBUG: f=%v, d=%v, w=%v, k=%d\n", allele.FitnessEffect, d, bin_width, k); debugI++ }
		if k > 0 && k <= 50 {
			fitness_bins[k] += float64(allele.Count)	// we had this many of the same id, so same fitness
		} /*else {
			config.Verbose(1, "k out of range: %d", k)
		}*/
	}
}
