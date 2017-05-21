package pop

import (
	"bitbucket.org/geneticentropy/mendel-go/dna"
	"bitbucket.org/geneticentropy/mendel-go/config"
	"bitbucket.org/geneticentropy/mendel-go/utils"
	"bitbucket.org/geneticentropy/mendel-go/random"
	"log"
	"math/rand"
)


// Individual represents 1 organism in the population, tracking its mutations and alleles.
type Individual struct {
	Pop             *Population
	GenoFitness     float64		// fitness due to genomic mutations
	PhenoFitness     float64		// fitness due to GenoFitness plus environmental noise and selection noise
	Dead            bool 		// if true, selection has identified it for elimination
	// we are not currently modeling chromosomes, only a big array of LBs
	LinkagesFromDad []*dna.LinkageBlock
	LinkagesFromMom []*dna.LinkageBlock
}


func IndividualFactory(pop *Population) *Individual{
	ind := &Individual{
		Pop: pop,
		//Chromos: make([]*dna.Chromosome, config.Cfg.Population.Haploid_chromosome_number),
		LinkagesFromDad: make([]*dna.LinkageBlock, config.Cfg.Population.Num_linkage_subunits),
		LinkagesFromMom: make([]*dna.LinkageBlock, config.Cfg.Population.Num_linkage_subunits),
	}
	// Note: there is no need to allocate the mutation slices with backing arrays. That will happen automatically the 1st time they are
	//		appended to with append(). Altho we will prob eventually want to implement our own append function to do it in bigger chunks.
	//		See https://blog.golang.org/go-slices-usage-and-internals

	//todo: there is probably a faster way to initialize these arrays
	for i := range ind.LinkagesFromDad { ind.LinkagesFromDad[i] = dna.LinkageBlockFactory() }
	for i := range ind.LinkagesFromMom { ind.LinkagesFromMom[i] = dna.LinkageBlockFactory() }

	return ind
}


// GetNumLinkages returns the number of linkage blocks from each parent (we assume they always have the same number of LBs frome each parent)
func (ind *Individual) GetNumLinkages() uint32 { return uint32(len(ind.LinkagesFromDad)) }


// Mate combines this person with the specified person to create a list of offspring.
func (ind *Individual) Mate(otherInd *Individual, uniformRandom *rand.Rand) []*Individual {
	if RecombinationType(config.Cfg.Population.Recombination_model) != FULL_SEXUAL { utils.NotImplementedYet("Recombination models other than FULL_SEXUAL are not yet supported") }
	actual_offspring := Mdl.CalcNumOffspring(ind, uniformRandom)
	//config.Verbose(9, " actual_offspring=%d", actual_offspring)
	offspr := make([]*Individual, actual_offspring)
	for child:=uint32(0); child<actual_offspring; child++ {
		offspr[child] = ind.OneOffspring(otherInd, uniformRandom)
	}
	return offspr
}


// Offspring returns 1 offspring of this person and the specified person.
// We assume ind is the dad and otherInd is the mom.
func (ind *Individual) OneOffspring(otherInd *Individual, uniformRandom *rand.Rand) *Individual {
	offspr := IndividualFactory(ind.Pop)
	//todo: support non dynamic linkage...
	// Set the number of segments.  Three linkgage blocks of the chromosome that are involved in the crossover.  Form the gametes chromosome by chromosome.
	//iseg_max := 3
	//if !config.Cfg.Population.Dynamic_linkage {
	//	iseg_max = 1  // can come from any parent
	//}

	// Inherit linkage blocks
	//chr_length := config.Cfg.Population.Num_linkage_subunits / config.Cfg.Population.Haploid_chromosome_number 		// num LBs in each chromosome
	//for chr:=1; chr<=config.Cfg.Population.Haploid_chromosome_number; chr++ {
	for lb:=uint32(0); lb<ind.GetNumLinkages(); lb++ {
		// randomly choose which grandparents to get the LBs from
		if uniformRandom.Intn(2) == 0 {
			offspr.LinkagesFromDad[lb] = ind.LinkagesFromDad[lb].Copy()
		} else {
			offspr.LinkagesFromDad[lb] = ind.LinkagesFromMom[lb].Copy()
		}

		if uniformRandom.Intn(2) == 0 {
			offspr.LinkagesFromMom[lb] = otherInd.LinkagesFromDad[lb].Copy()
		} else {
			offspr.LinkagesFromMom[lb] = otherInd.LinkagesFromMom[lb].Copy()
		}
	}

	// Apply new mutations
	numMutations := Mdl.CalcNumMutations(uniformRandom)
	for m:=uint32(1); m<=numMutations; m++ {
		lb := uniformRandom.Intn(int(ind.GetNumLinkages()))	// choose a random LB index

		// Randomly choose the LB from dad or mom to put the mutation in.
		// Note: AppendMutation() creates a mutation with deleterious/neutral/favorable, dominant/recessive, etc. based on the relevant input parameter rates
		if uniformRandom.Intn(2) == 0 {
			offspr.LinkagesFromDad[lb].AppendMutation(uniformRandom)
		} else {
			offspr.LinkagesFromMom[lb].AppendMutation(uniformRandom)
		}
	}
	//d, n, f := offspr.GetNumMutations()
	//config.Verbose(9, "my mutations including new ones: %d, %d, %d", d, n, f)

	offspr.GenoFitness = Mdl.CalcIndivFitness(offspr) 		// store resulting fitness
	if offspr.GenoFitness <= 0.0 { offspr.Dead = true }

	return offspr
}


// Various algorithms for determining the random number of offspring for an individual
type CalcNumOffspringType func(ind *Individual, uniformRandom *rand.Rand) uint32

// A uniform algorithm for calculating the number of offspring that gives an even distribution between 1 and 2*Num_offspring-1
func CalcUniformNumOffspring(ind *Individual, uniformRandom *rand.Rand) uint32 {
	// If Num_offspring is 4.5, we want a range from 1-8
	maxRange := (2 * ind.Pop.Num_offspring) - 2 		// subtract 2 to get a buffer of 1 at each end
	numOffspring := uniformRandom.Float64() * maxRange 		// some float between 0 and maxRange
	return uint32(random.Round(uniformRandom, numOffspring + 1)) 	// shift it so it is between 1 and maxRange+1, then get to an uint32
}


// Randomly rounds the desired number of offspring to the integer below or above, proportional to how close it is to each (so the resulting average should be Num_offspring)
func CalcSemiFixedNumOffspring(ind *Individual, uniformRandom *rand.Rand) uint32 {
	return uint32(random.Round(uniformRandom, ind.Pop.Num_offspring))
}


// An algorithm taken from the fortran mendel for calculating the number of offspring
func CalcFortranNumOffspring(ind *Individual, uniformRandom *rand.Rand) uint32 {
	//todo: i do not understand some of this logic, it is from lines 64-73 of mating.f90
	actual_offspring := uint32(ind.Pop.Num_offspring)		// truncate num offspring to integer
	if ind.Pop.Num_offspring - float64(uint32(ind.Pop.Num_offspring)) > uniformRandom.Float64() { actual_offspring++ }	// randomly round it up sometimes
	//if indivIndex == 1 { actual_offspring = utils.Max(1, actual_offspring) } 	// assuming this was some special case specific to the fortran implementation
	actual_offspring = utils.MinUint32(uint32(ind.Pop.Num_offspring+1), actual_offspring) 	// does not seem like this line does anything, because actual_offspring will always be uint32(ind.Pop.Num_offspring)+1 or uint32(ind.Pop.Num_offspring)
	return actual_offspring
}


// Randomly choose a number of offspring that is, on average, proportional to the individual's fitness
func CalcFitnessNumOffspring(ind *Individual, uniformRandom *rand.Rand) uint32 {
	// in the fortran version this is controlled by fitness_dependent_fertility
	utils.NotImplementedYet("CalcFitnessNumOffspring not implemented yet")
	return uint32(random.Round(uniformRandom, ind.Pop.Num_offspring))
}


// Algorithms for determining the number of additional mutations a specific offspring should be given
type CalcNumMutationsType func(uniformRandom *rand.Rand) uint32

// Randomly round Mutn_rate to the uint32 below or above, proportional to how close it is to each (so the resulting average should be Mutn_rate)
func CalcSemiFixedNumMutations (uniformRandom *rand.Rand) uint32 {
	numMutations := uint32(random.Round(uniformRandom, config.Cfg.Mutations.Mutn_rate))
	return numMutations
}

// Use a poisson distribution to choose a number of mutations, with the mean of number of mutations for all individuals being Mutn_rate
func CalcPoissonNumMutations (uniformRandom *rand.Rand) uint32 {
	return uint32(random.Poisson(uniformRandom, config.Cfg.Mutations.Mutn_rate))
}


// Algorithms for aggregating all of the individual's mutation fitness factors into a single geno fitness value
type CalcIndivFitnessType func(ind *Individual) float64

// SumIndivFitness adds together the fitness factors of all of the mutations. An individual's fitness starts at 1 and then deleterious
// mutations subtract from that and favorable mutations add to it. A total fitness of 0 means the individual is dead.
func SumIndivFitness(ind *Individual) (fitness float64) {
	// Sum all the LB fitness numbers
	fitness = 1.0
	for _, lb := range ind.LinkagesFromDad {
		// Note: the deleterious mutation fitness factors are already negative
		fitness += lb.SumFitness()
		//for _, m := range lb.DMutn { if (m.GetExpressed()) { fitness += m.GetFitnessEffect() } }
		//for _, m := range lb.FMutn { if (m.GetExpressed()) { fitness += m.GetFitnessEffect() } }
	}
	for _, lb := range ind.LinkagesFromMom {
		fitness += lb.SumFitness()
		//for _, m := range lb.DMutn {	if (m.GetExpressed()) { fitness += m.GetFitnessEffect() } }
		//for _, m := range lb.FMutn {	if (m.GetExpressed()) { fitness += m.GetFitnessEffect() } }
	}
	return
}

// MultIndivFitness aggregates the fitness factors of all of the mutations using a combination of additive and mutliplicative,
// based on config.Cfg.Mutations.Multiplicative_weighting
func MultIndivFitness(_ *Individual) (fitness float64) {
	fitness = 1.0
	//todo: do not know the exact formula to use for this yet
	utils.NotImplementedYet("Multiplicative_weighting not implemented yet")
	return fitness
}


// GetMutationStats returns the number of deleterious, neutral, favorable mutations, and the average fitness factor of each
func (ind *Individual) GetMutationStats() (deleterious, neutral, favorable uint32, avDelFit, avFavFit float64) {
	// Calc the average of each type of mutation: multiply the average from each LB and num mutns from each LB, then at the end divide by total num mutns
	for _,lb := range ind.LinkagesFromDad {
		delet, neut, fav, avD, avF := lb.GetMutationStats()
		deleterious += delet
		neutral += neut
		favorable += fav
		avDelFit += (float64(delet) * avD)
		avFavFit += (float64(fav) * avF)
	}
	for _,lb := range ind.LinkagesFromMom {
		delet, neut, fav, avD, avF := lb.GetMutationStats()
		deleterious += delet
		neutral += neut
		favorable += fav
		avDelFit += (float64(delet) * avD)
		avFavFit += (float64(fav) * avF)
	}
	if deleterious > 0 { avDelFit = avDelFit / float64(deleterious) }
	if favorable > 0 { avFavFit = avFavFit / float64(favorable) }
	return
}

// Report prints out statistics of this individual. If final==true is prints more details.
func (ind *Individual) Report(_ bool) {
	deleterious, neutral, favorable, avDelFit, avFavFit := ind.GetMutationStats()
	log.Printf("  Ind: fitness: %v, mutations: %d, deleterious: %d, neutral: %d, favorable: %d, avg del: %v, avg fav: %v", ind.GenoFitness, deleterious+neutral+favorable, deleterious, neutral, favorable, avDelFit, avFavFit)
}
